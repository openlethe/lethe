package metrics

import (
	"strings"
	"testing"
)

func TestRegistryExpose(t *testing.T) {
	Inc("test_counter_a")
	GetCounter("test_counter_a").Inc()
	SetGauge("test_gauge_a", 42)
	out := Expose()
	if !strings.Contains(out, "# TYPE test_counter_a counter\ntest_counter_a 2\n") {
		t.Fatalf("counter exposition wrong:\n%s", out)
	}
	if !strings.Contains(out, "# TYPE test_gauge_a gauge\ntest_gauge_a 42\n") {
		t.Fatalf("gauge exposition wrong:\n%s", out)
	}
}

func TestCounterIdentity(t *testing.T) {
	a := GetCounter("test_counter_b")
	b := GetCounter("test_counter_b")
	if a != b {
		t.Fatal("registry returned distinct counters for one name")
	}
	a.Inc()
	if GetCounter("test_counter_b").v.Load() != 1 {
		t.Fatal("increment not shared")
	}
}
