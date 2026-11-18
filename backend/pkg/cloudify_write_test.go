package pkg

import "testing"

func TestCloudifyBodyError(t *testing.T) {
	cases := []struct {
		body   string
		failed bool
	}{
		{`{"code":200,"message":"ok","data":{"id":1}}`, false},
		{`{"status":"error","message":"MA_HANG not found"}`, true},
		{`{"success":false,"msg":"bad"}`, true},
		{`{"jsonrpc":"2.0","error":{"code":200,"data":{"message":"ValidationError"}}}`, true},
		{`{"error":null,"data":{}}`, false},
		{`[1,2]`, false},
		{`{"code":400,"name":"Bad Request","description":"<p>Mặt hàng có số lượng vượt quá</p>"}`, true},
		{`{"code":200,"message":"Success","data":{"id":25458}}`, false},
	}
	for _, tc := range cases {
		if _, failed := cloudifyBodyError([]byte(tc.body)); failed != tc.failed {
			t.Errorf("cloudifyBodyError(%s) failed=%v, want %v", tc.body, failed, tc.failed)
		}
	}
}
