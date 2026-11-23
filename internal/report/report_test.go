package report

import (
	"math"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 0.005 }

func TestWilson(t *testing.T) {
	// Reference values from the Wilson score formula at z = 1.96.
	cases := []struct {
		k, n   int
		lo, hi float64
	}{
		{8, 10, 0.4902, 0.9433},
		{0, 10, 0, 0.2775},
		{10, 10, 0.7225, 1},
		{36, 72, 0.3874, 0.6126},
	}
	for _, c := range cases {
		lo, hi := Wilson(c.k, c.n)
		if !near(lo, c.lo) || !near(hi, c.hi) {
			t.Errorf("Wilson(%d, %d) = %.4f-%.4f, want %.4f-%.4f", c.k, c.n, lo, hi, c.lo, c.hi)
		}
	}
	if lo, hi := Wilson(0, 0); lo != 0 || hi != 0 {
		t.Error("an empty sample has no interval")
	}
}

func TestKappa(t *testing.T) {
	if k := Kappa(40, 0, 0, 60); k == nil || !near(*k, 1) {
		t.Errorf("perfect agreement: %v", k)
	}
	// Agreement exactly at chance level: both raters say yes half the time, independently.
	if k := Kappa(25, 25, 25, 25); k == nil || !near(*k, 0) {
		t.Errorf("chance agreement: %v", k)
	}
	// 90% raw agreement, but mostly on the common label.
	if k := Kappa(85, 5, 5, 5); k == nil || !near(*k, 0.4444) {
		t.Errorf("imbalanced labels: %v", *k)
	}
	if k := Kappa(10, 0, 0, 0); k != nil {
		t.Error("kappa is undefined when both raters always say yes")
	}
}

func TestPercentile(t *testing.T) {
	xs := []int64{100, 400, 200, 300, 500, 900, 800, 700, 600, 1000}
	if p := percentile(xs, 0.5); p != 500 {
		t.Errorf("p50 = %d", p)
	}
	if p := percentile(xs, 0.95); p != 1000 {
		t.Errorf("p95 = %d", p)
	}
	if xs[0] != 100 {
		t.Error("percentile must not reorder its input")
	}
}
