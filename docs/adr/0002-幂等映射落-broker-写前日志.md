# 0002 · 幂等映射落在 broker.order_acks（写前日志，不是订单真相）

- **状态**：已裁决（2026-10-23，若曦；解除 ARCHITECTURE §9.7 R2 的「待裁」）
- **场景**：
  1. `PlaceOrder` 的契约要求「同 ID 同载荷 → 原 `OrderAck` + `IdempotentReplay`」「同 ID 不同载荷 → `duplicate_order`」（`api-schema.md` §3）。这需要**本地**保存载荷指纹与当初的回执——柜台原生 `client-order-id` 最多拒绝重复、回带 ID，既重放不出当初的 ack，也比不了指纹。
  2. 旧 `AckStore` 只有 `Load` + `Save`，两步之间没有原子性。实测（一次性脚手架，2026-10-23）：两个并发同 `ClientOrderID` 调用都读到未命中时**各发一笔**，同一 ID 产生 2 笔成交；把 Store 的临界区拆开后该用例 16/16 复现。这直接违反 `dataflow.md` §4 不变量 1。
  3. core 的 `pkg/live/order_store.go` 以 `order_id` 为主键，没有 `client_order_id` / 指纹 / ack 列；`PostgresOrderStore` 写的 `public.orders` 在 core 的 `migrationStatements()` 里**没有 DDL**。订单归属 U2 也未裁。
- **决策**：
  1. 幂等映射的持久落点是本仓自有的 **`broker.order_acks`**（结构见 `db-schema.md` §1.1），经 `gateway.AckStore` port 注入；网关仍不直连任何表。
  2. `AckStore` 改为四方法：`Load`（只读）、`Reserve`（**原子占位**，`created=true` 才拿到发送权）、`SaveAck`（只写已占位的行）、`Durable`（是否跨重启持久）。
  3. PG 实现的发送权判定用一条 `INSERT ... ON CONFLICT (account_id, client_order_id) DO NOTHING RETURNING`；冲突后回读依赖默认的 READ COMMITTED。
  4. `NewXTPTrader` 要求 `Acks.Durable() == true`：`MemoryAckStore` 等内存实现**构造期拒绝**，「无幂等键不接真钱」不再靠装配方记得。
  5. 柜台原生 `client-order-id` 只作**第二道防线**，随 X6c 全量接入；它不承担重放与指纹判重。
- **理由**：
  - 否掉「柜台侧回执单独承担」：能力上做不到第 1 条，且仓内没有 XTP SDK（`pkg/live/broker/xtp/xtp.go` 是 skeleton，`client-order-id` 零命中），柜台去重行为今天不可验证。
  - 否掉「注入 core order store」：core 今天无表、无对应列、U2 未裁；X6d 独立进程后进程内注入不再成立，改走网络则给每笔下单加一跳。
  - 否掉「本地文件 WAL」：只有单写者 / 单机持久；真钱前仍要换 PG，等于做两遍；手写 fsync 与半写记录是新风险。
  - 选 PG：跨重启 / 跨进程、可备份、可查保留期；PG 不可用 → `idempotency_store_unavailable` 且不发送，与既有 fail-closed 语义一致。
- **代价 / 后果**：
  1. 本仓从「零业务表」变为「持有一张通道自有表」。这张表**不是订单真相**：不存成交 / 持仓 / 费用；丢失 = 重复下单风险，必须进备份策略。
  2. 实盘下单依赖 PG 可用性（不可用即不发送，这是契约要求，不是退化）。
  3. 新增 `github.com/jackc/pgx/v5 v5.7.3` 依赖（AGENTS §4 登记；不引 `kit/db`，避免为一次连接多拉一份模块图）。
  4. 装配方必须在启动时调用 `ackstore.Migrate`；本仓今天没有二进制，执行入口留给将来的 X6d / 装配。X6d 按同日裁决**暂缓**（仓内文档仍把它列为触发式选项）。
  5. 保留期未定：当前不自动删除；删早了等于允许重复下单，先登记为待办而不是拍一个数。
  6. 机械检查：`internal/ackstore` 的 PG 集成测试（`BROKER_TEST_DSN`）钉住「并发只出一个 winner / 跨实例回读 / 无占位不许写回执」；内存实现的并发腿在 `internal/gateway`。
- **替代关系**：无。本条替代的是 `ARCHITECTURE.md` §9.7 R2 的「待裁」状态，不推翻 ADR 0001（先 Mock 再真钱）。
