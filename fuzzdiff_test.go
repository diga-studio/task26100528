package risktrend

import (
	"fmt"
	"testing"
)

// 随机差分测试：小规模随机员工/历史/交错写入与查询，
// 每次查询都与从全部记录独立重算的结果逐一比对。
func TestRandomizedDifferential(t *testing.T) {
	for seed := uint64(1); seed <= 200; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := xorshift(seed*7919 + 13)
			nEmp := 1 + int(rng.intn(8))
			emps := make([]Employee, nEmp)
			for i := range emps {
				emps[i] = Employee{
					ID:      fmt.Sprintf("e%d", i),
					Weight:  1 + int(rng.intn(100)),
					Initial: int(rng.intn(101)),
				}
			}
			yearSec := int64(RangeDays) * secondsPerDay
			// 小时间窗制造大量同秒与相邻事件。
			window := int64(1+rng.intn(400)) * secondsPerDay
			if window > yearSec {
				window = yearSec
			}
			nHist := int(rng.intn(60))
			history := make([]Assessment, 0, nHist+200)
			for i := 0; i < nHist; i++ {
				history = append(history, Assessment{
					EmployeeID: emps[rng.intn(int64(nEmp))].ID,
					Time:       testStart + rng.intn(window),
					Score:      int(rng.intn(101)),
				})
			}
			s, err := New(testStart, emps, history)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			for step := 0; step < 200; step++ {
				if rng.intn(2) == 0 {
					a := Assessment{
						EmployeeID: emps[rng.intn(int64(nEmp))].ID,
						Time:       testStart + rng.intn(window),
						Score:      int(rng.intn(101)),
					}
					if err := s.AddAssessment(a); err != nil {
						t.Fatalf("AddAssessment(%+v): %v", a, err)
					}
					history = append(history, a)
				} else {
					from := testStart + rng.intn(window+1)
					to := testStart + rng.intn(window+1)
					if from > to {
						from, to = to, from
					}
					tls := buildRef(emps, history)
					wantAvg, wantRisk := refQuery(tls, from, to)
					gotAvg, gotRisk := s.Query(from, to)
					if gotRisk != wantRisk || !almostEqual(gotAvg, wantAvg) {
						t.Fatalf("step %d: Query(%d, %d) = (%v, %d), want (%v, %d)",
							step, from, to, gotAvg, gotRisk, wantAvg, wantRisk)
					}
				}
			}
		})
	}
}
