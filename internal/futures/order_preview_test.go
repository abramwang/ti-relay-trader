package futures

import (
	"strings"
	"testing"
)

func TestOrderPreviewDecode(t *testing.T) {
	raw := `{"account_id":"300052141","security_id":"RB2701.SHFE","trade_date":"20261119","trade_side":"B","offset_type":"O","hedge_type":"speculation","qty":1,"price":"3000.000000000"}`
	r, err := DecodeOrderPreview([]byte(raw))
	if err != nil || r.Price != "3000.000000000" || ValidatePreviewPrice(r, "1") != nil {
		t.Fatal(r, err)
	}
	for _, broken := range []string{
		strings.Replace(raw, `"qty":1`, `"qty":1.0`, 1),
		strings.Replace(raw, `"qty":1`, `"qty":0`, 1),
		strings.Replace(raw, `"qty":1`, `"qty":2147483648`, 1),
		strings.Replace(raw, `"price":"3000.000000000"`, `"price":3000`, 1),
		strings.Replace(raw, `"price":"3000.000000000"`, `"price":"0"`, 1),
		strings.Replace(raw, `"price":"3000.000000000"`, `"price":"3e3"`, 1),
		strings.Replace(raw, `"trade_side":"B"`, `"trade_side":"R"`, 1),
		strings.Replace(raw, `"trade_date":"20261119"`, `"trade_date":"20260230"`, 1),
		strings.Replace(raw, `"qty":1`, `"qty":1,"qty":2`, 1),
		strings.Replace(raw, `"qty":1`, `"qty":1,"execution_permission":true`, 1),
		raw + `{}`, strings.Repeat(" ", 4096) + raw,
	} {
		if _, err := DecodeOrderPreview([]byte(broken)); err == nil {
			t.Fatal("accepted invalid intent", broken)
		}
	}
	r.Price = "3000.5"
	if ValidatePreviewPrice(r, "1") == nil {
		t.Fatal("accepted off-tick price")
	}
	r.Price = "9000"
	if ValidatePreviewPrice(r, "1") != nil {
		t.Fatal("preview must not apply broker dynamic limits")
	}
}
