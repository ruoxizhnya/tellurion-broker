package gateway

import (
	"encoding/json"
	"testing"
	"time"
)

// TestWireShapeUnchangedByContractMigration 钉住 CT-05 迁移的判据：
// 同一条报文快照改前 / 改后**逐字节一致**。
//
// golden 是在本地 DTO 仍是独立结构体时（contracts 尚未引入）实测抓取的；
// 迁移成 `tellurion-contracts/broker` 类型别名后，任何字段名、顺序、
// omitempty 或空值处理的漂移都会红。
func TestWireShapeUnchangedByContractMigration(t *testing.T) {
	at := time.Date(2026, 11, 7, 9, 30, 0, 0, time.UTC)
	cases := []struct {
		id     string
		v      any
		golden string
	}{
		{
			"order_request_market",
			OrderRequest{ClientOrderID: "c-1", AccountID: "A1", Symbol: "000001.SZ", Side: SideBuy, OrderType: OrderMarket, Quantity: 100},
			`{"client_order_id":"c-1","account_id":"A1","symbol":"000001.SZ","side":"buy","order_type":"market","quantity":100}`,
		},
		{
			"order_request_limit",
			OrderRequest{ClientOrderID: "c-2", AccountID: "A1", Symbol: "600000.SH", Side: SideSell, OrderType: OrderLimit, Quantity: 200, LimitPrice: 10.5},
			`{"client_order_id":"c-2","account_id":"A1","symbol":"600000.SH","side":"sell","order_type":"limit","quantity":200,"limit_price":10.5}`,
		},
		{
			"order_ack",
			OrderAck{ClientOrderID: "c-1", BrokerOrderID: "XTP-1", Status: OrderFilled, IdempotentReplay: false, AcceptedAt: at},
			`{"client_order_id":"c-1","broker_order_id":"XTP-1","status":"filled","idempotent_replay":false,"accepted_at":"2026-11-07T09:30:00Z"}`,
		},
		{
			"order_ack_replay",
			OrderAck{ClientOrderID: "c-1", Status: OrderAccepted, IdempotentReplay: true, AcceptedAt: at, Code: "duplicate_order", Reason: "replay"},
			`{"client_order_id":"c-1","status":"accepted","idempotent_replay":true,"accepted_at":"2026-11-07T09:30:00Z","code":"duplicate_order","reason":"replay"}`,
		},
		{
			"cancel_request",
			CancelRequest{ClientOrderID: "c-1"},
			`{"client_order_id":"c-1"}`,
		},
		{
			"order_state",
			OrderState{ClientOrderID: "c-1", BrokerOrderID: "XTP-1", Status: OrderPartiallyFilled, FilledQuantity: 50, AvgFillPrice: 10.25, RemainingQuantity: 50, LastUpdate: at},
			`{"client_order_id":"c-1","broker_order_id":"XTP-1","status":"partially_filled","filled_quantity":50,"avg_fill_price":10.25,"remaining_quantity":50,"last_update":"2026-11-07T09:30:00Z"}`,
		},
		{
			"fill",
			Fill{FillID: "f-1", ClientOrderID: "c-1", BrokerOrderID: "XTP-1", Symbol: "000001.SZ", Side: SideBuy, Quantity: 50, Price: 10.25, Commission: 5, StampTax: 0, TransferFee: 0.01, FilledAt: at},
			`{"fill_id":"f-1","client_order_id":"c-1","broker_order_id":"XTP-1","symbol":"000001.SZ","side":"buy","quantity":50,"price":10.25,"commission":5,"stamp_tax":0,"transfer_fee":0.01,"filled_at":"2026-11-07T09:30:00Z"}`,
		},
		{
			"position",
			Position{AccountID: "A1", Symbol: "000001.SZ", Quantity: 100, AvailableQuantity: 0, AvgCost: 10, MarketValue: 1025, AsOf: at},
			`{"account_id":"A1","symbol":"000001.SZ","quantity":100,"available_quantity":0,"avg_cost":10,"market_value":1025,"as_of":"2026-11-07T09:30:00Z"}`,
		},
		{
			"account_snapshot",
			AccountSnapshot{AccountID: "A1", AsOf: at, Cash: Cash{Total: 100000, Available: 99000}, TotalAssets: 101025, Positions: []Position{}, Fees: Fees{Commission: 5, StampTax: 0, TransferFee: 0.01}},
			`{"account_id":"A1","as_of":"2026-11-07T09:30:00Z","cash":{"total":100000,"available":99000},"total_assets":101025,"positions":[],"fees":{"commission":5,"stamp_tax":0,"transfer_fee":0.01}}`,
		},
		{
			"flattened_position",
			FlattenedPosition{AccountID: "A1", Symbol: "000001.SZ", Quantity: 100, ClientOrderID: "c-9", BrokerOrderID: "XTP-9", AvgFillPrice: 10.4, FilledAt: at},
			`{"account_id":"A1","symbol":"000001.SZ","quantity":100,"client_order_id":"c-9","broker_order_id":"XTP-9","avg_fill_price":10.4,"filled_at":"2026-11-07T09:30:00Z"}`,
		},
		{
			"skipped_position",
			SkippedPosition{AccountID: "A1", Symbol: "600000.SH", Quantity: 200, Reason: "channel_down"},
			`{"account_id":"A1","symbol":"600000.SH","quantity":200,"reason":"channel_down"}`,
		},
		{
			"emergency_flatten_result",
			EmergencyFlattenResult{Sold: []FlattenedPosition{}, Skipped: []SkippedPosition{}, SoldTotal: 0, StartedAt: at, CompletedAt: at, Reason: "kill-switch"},
			`{"sold":[],"skipped":[],"sold_total":0,"started_at":"2026-11-07T09:30:00Z","completed_at":"2026-11-07T09:30:00Z","reason":"kill-switch"}`,
		},
		{
			"emergency_flatten_result_nonempty",
			EmergencyFlattenResult{
				Sold:        []FlattenedPosition{{AccountID: "A1", Symbol: "000001.SZ", Quantity: 100, ClientOrderID: "c-9", BrokerOrderID: "XTP-9", AvgFillPrice: 10.4, FilledAt: at}},
				Skipped:     []SkippedPosition{{AccountID: "A1", Symbol: "600000.SH", Quantity: 200, Reason: "channel_down"}},
				SoldTotal:   1040,
				StartedAt:   at,
				CompletedAt: at,
				Reason:      "kill-switch",
			},
			`{"sold":[{"account_id":"A1","symbol":"000001.SZ","quantity":100,"client_order_id":"c-9","broker_order_id":"XTP-9","avg_fill_price":10.4,"filled_at":"2026-11-07T09:30:00Z"}],"skipped":[{"account_id":"A1","symbol":"600000.SH","quantity":200,"reason":"channel_down"}],"sold_total":1040,"started_at":"2026-11-07T09:30:00Z","completed_at":"2026-11-07T09:30:00Z","reason":"kill-switch"}`,
		},
	}
	for _, c := range cases {
		b, err := json.Marshal(c.v)
		if err != nil {
			t.Fatalf("%s: marshal: %v", c.id, err)
		}
		if string(b) != c.golden {
			t.Errorf("%s wire shape drifted:\n got: %s\nwant: %s", c.id, b, c.golden)
		}
	}
}
