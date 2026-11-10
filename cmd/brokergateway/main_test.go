package main

import "testing"

func TestParseMockPrices(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    map[string]float64
		wantErr bool
	}{
		{name: "empty means no prices", raw: "", want: nil},
		{name: "blank means no prices", raw: "  ", want: nil},
		{name: "single", raw: "000001=12.5", want: map[string]float64{"000001": 12.5}},
		{
			name: "multiple with spaces",
			raw:  " 000001=12.5, 600000=8.2 ,002594=30.1 ",
			want: map[string]float64{"000001": 12.5, "600000": 8.2, "002594": 30.1},
		},
		{name: "missing equals", raw: "000001", wantErr: true},
		{name: "empty symbol", raw: "=12.5", wantErr: true},
		{name: "non numeric price", raw: "000001=abc", wantErr: true},
		{name: "zero price", raw: "000001=0", wantErr: true},
		{name: "negative price", raw: "000001=-1", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseMockPrices(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got prices %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for sym, px := range tc.want {
				if got[sym] != px {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}
