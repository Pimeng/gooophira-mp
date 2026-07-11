package netutil

import (
	"net/http"
	"reflect"
	"testing"
)

func TestMaxIdleConns(t *testing.T) {
	if MaxIdleConns != 100 {
		t.Errorf("MaxIdleConns = %d, want 100", MaxIdleConns)
	}
}

func TestProxyDirectIgnoresEnvironment(t *testing.T) {
	proxyMu.Lock()
	origURL, origDirect := proxyURL, proxyDirect
	proxyMu.Unlock()
	defer SetProxy(origURL, origDirect)
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:7890")
	SetProxy("", true)
	req, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := proxyFunc()(req)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("direct proxy = %v, want nil", got)
	}
}

func TestDefaultDNSServers(t *testing.T) {
	// 恢复默认并保留，确保不受其他测试执行顺序影响。
	orig := append([]string(nil), dnsServers...)
	defer func() {
		dnsServers = append([]string(nil), orig...)
	}()
	dnsServers = append([]string(nil), defaultDNSServers...)

	want := []string{"1.1.1.1:53", "8.8.8.8:53"}
	if !reflect.DeepEqual(dnsServers, want) {
		t.Errorf("default dnsServers = %v, want %v", dnsServers, want)
	}
}

func TestSetDNSServers(t *testing.T) {
	// 保存并 defer 恢复默认，避免污染全局状态。
	orig := append([]string(nil), dnsServers...)
	defer func() {
		dnsServers = append([]string(nil), orig...)
	}()

	SetDNSServers([]string{"9.9.9.9:53", "149.112.112.112:53"})
	got := currentDNSServers()
	want := []string{"9.9.9.9:53", "149.112.112.112:53"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("currentDNSServers after Set = %v, want %v", got, want)
	}

	// 验证原切片未被外部修改影响（Set 做了拷贝）。
	want[0] = "0.0.0.0:53"
	got = currentDNSServers()
	if got[0] == "0.0.0.0:53" {
		t.Error("SetDNSServers should copy the slice, not share backing array")
	}
}

func TestSetDNSServers_EmptyIgnored(t *testing.T) {
	orig := append([]string(nil), dnsServers...)
	defer func() {
		dnsServers = append([]string(nil), orig...)
	}()

	SetDNSServers([]string{})
	if !reflect.DeepEqual(dnsServers, orig) {
		t.Errorf("empty SetDNSServers should be ignored, got %v", dnsServers)
	}

	SetDNSServers(nil)
	if !reflect.DeepEqual(dnsServers, orig) {
		t.Errorf("nil SetDNSServers should be ignored, got %v", dnsServers)
	}
}

func TestCurrentDNSServers_Copy(t *testing.T) {
	orig := append([]string(nil), dnsServers...)
	defer func() {
		dnsServers = append([]string(nil), orig...)
	}()

	SetDNSServers([]string{"1.1.1.1:53"})
	got := currentDNSServers()
	got[0] = "mutated"
	if dnsServers[0] == "mutated" {
		t.Error("currentDNSServers should return a copy")
	}
}
