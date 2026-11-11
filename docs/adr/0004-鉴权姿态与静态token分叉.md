# 0004 · 鉴权姿态：写侧静态 service token、读侧只读且无鉴权（与 kit JWT 体系的显式分叉例外）

- **状态**：已裁决（2026-11-12，`BROKER-AUTH-ADR`；证据 ODR-069 `tellurion-docs:docs/archive/odr/odr-069-audit-wave2-core-architecture.md`，裁决 D11=b）
- **关系**：不替代 0001 / 0002 / 0003。与 [`0003 真钱通道暂不实施`](0003-真钱通道暂不实施与重开条件.md) 联动——真钱形态落地时必须重裁本条（见下文「重开条件」）。
- **背景**：本仓的鉴权姿态此前**只散在代码注释里**：`internal/httpapi/httpapi.go:74-89` 的 `requireToken` 对 3 条写端点做静态 token 校验，5 条 GET 读端点完全不鉴权。兄弟仓（data / risk / analytics / strategy / orchestration）统一走 `tellurion-kit` 的 `auth/httpadapter`（JWT service token + 策略表）。同一工作区里两种鉴权体系并存，却没有一条 ADR 说明**这是裁决还是遗漏**——本 ADR 把它固化下来并显式登记分叉。
- **决策**：
  1. **写侧（3 条）用静态 service token**：`POST /api/broker/orders`、`DELETE /api/broker/orders/{clientOrderID}`、`POST /api/broker/emergency-flatten` 要求 `Authorization: Bearer $BROKER_SERVICE_TOKEN`，比较用 `crypto/subtle.ConstantTimeCompare`。
  2. **空 token = 写面关闭**（fail-closed）：`BROKER_SERVICE_TOKEN` 未配置时写侧返回 `503 writes_disabled`，**不是**「不校验」。token 不匹配返回 `401 unauthorized`（带 `WWW-Authenticate: Bearer`）。
  3. **读侧（5 条）只读、不做鉴权**：`GET` 查单 / 持仓 / 账户 / 成交 / 对账快照。理由是三重叠加：① 全部只读、天然幂等，不产生副作用；② 报文面不含凭证类敏感数据（账户号 / 持仓 / 成交都是本机运维需要的口径）；③ 部署层已用**回环绑定 + 端口隔离**封闭可达面——`deploy/compose.yaml` 与本地栈都只把 `:8091` 绑在 `127.0.0.1` / 服务内网，不做公网或 NodePort 暴露。**这条前提是决策的一部分，不是可选项**：一旦有人把端口暴露出去，本决策即失效（见「代价 / 后果」与「重开条件」）。
  4. **与 kit JWT 体系的分叉是显式接受的例外，不是遗漏**：本仓**本轮不引入** `tellurion-kit` 的 `auth/httpadapter`，也不自建 JWT / 策略表。判据是「单人自托管 + Mock MVP + 一个调用方（core）」：静态 token 足够覆盖威胁模型（防误触发、防同机误连），而 JWT 体系的收益（多主体、可撤销、细粒度策略、密钥轮换）在本阶段的调用关系里无对应需求。与其为了「一致性」引一套没人用的鉴权栈，不如把分叉写成 ADR 供评审。
  5. **dev-only 边界**：本姿态只对「Mock + 回环/内网」有效，**不得表述为生产级鉴权**；token 明文只走进程级 / 用户级环境变量注入，禁止写入 `docker-compose.yml` / `.env` / 任何会入库的文件。
- **token 轮换**（操作步骤落在 [`../runbook.md`](../runbook.md) §2.1）：
  - 实现只有**一个 token 槽位**、无热加载：轮换 = 改环境变量 + 重启网关。
  - 因此**不存在双 token 并存的平滑窗口**。兼容窗口 = 网关重启到调用方换新 token 之间的间隔，期间写请求返回 `401 unauthorized`（显式失败，不静默放行）。
  - 轮换必须在**无在途写单**的时点执行；若期间已产生 `unknown_state`，按 runbook §5 排查，**严禁换 `client_order_id` 重发**。
  - 回滚 = 恢复旧 token 重启，同样受上面窗口约束。
- **代价 / 后果**：
  - 读侧无鉴权把「谁能连上 8091」直接等同于「谁能读账户与持仓」。这条风险由部署层承担，**不设代码级兜底**；回环绑定被破坏时没有任何技术拦阻。
  - 静态 token 不能按主体撤销、不能分权、不支持过期；泄露只能靠整体轮换（有写面中断窗口）。
  - 与兄弟仓的鉴权体系分叉会持续存在，跨仓审计会反复问「为什么 broker 不一样」——本 ADR 就是答案，重裁时必须更新本文件而不是口头解释。
- **否决的替代**：
  - **本轮迁 kit JWT**：需要引入 kit 依赖 + 策略表 + 密钥分发，收益不匹配当前单调用方形态，且会把一个纯文档单扩成跨仓改动（裁决 D11=b 明确本轮不做）。
  - **给读侧也加 token**：会让 core 的只读查询与运维排查全部多一道凭证分发，却不改变「端口可达即数据可见」的本质（凭证同样在本机）；真正有效的是端口隔离。
  - **不写 ADR，把决定留在代码注释里**：审计已经证明注释不足以回答「这是裁决还是漏了」，且注释无法约束未来改动。
- **重开条件（必须重裁，不得沿用本条）**：
  1. **X6c 真钱通道重开时**：真实柜台、真实资金、可能的多主体/多机部署，第 3、4 条的判据（回环隔离 + 单调用方）全部失效，必须重新裁决读写两侧鉴权形态，并按 0003 的重开流程同步更新本 ADR（标 `Superseded by 000N`）。
  2. **读侧路由出现写语义、或报文新增凭证类敏感字段**时：立即重裁读侧是否开鉴权。
  3. **`:8091` 出现回环/内网以外的暴露形态**（NodePort、Ingress、公网 LB）时：本条作废，不得以「已有 ADR」为由放行。
