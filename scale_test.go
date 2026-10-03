package risktrend

import (
	"fmt"
	"runtime"
	"testing"
	"time"
)

type xorshift uint64

func (x *xorshift) next() uint64 {
	v := uint64(*x)
	v ^= v << 13
	v ^= v >> 7
	v ^= v << 17
	*x = xorshift(v)
	return v
}

func (x *xorshift) intn(n int64) int64 { return int64(x.next() % uint64(n)) }

// 规模、耗时与内存：500 名员工（10 名全年只有起点分，490 名产生密集历史），
// 100 万条历史测评；随后交错执行 1 万次查询与 2000 次补录/覆盖，
// 其中过半写入发生在前 30 天（向年初补录稀疏员工并反复覆盖，影响延续到年末）。
func TestScalePerfMemory(t *testing.T) {
	const (
		nEmp       = 500
		nSparse    = 10
		nHistory   = 1_000_000
		nWrites    = 2_000
		nQueries   = 10_000
		timeBudget = 15 * time.Second
		memBudget  = 256 << 20
	)
	yearSec := int64(RangeDays) * secondsPerDay
	end := testStart + yearSec
	rng := xorshift(20240101)

	emps := make([]Employee, nEmp)
	for i := range emps {
		emps[i] = Employee{
			ID:      fmt.Sprintf("e%03d", i),
			Weight:  1 + int(rng.intn(100)),
			Initial: int(rng.intn(101)),
		}
	}
	// 密集历史仅来自后 490 名员工，分布刻意不均匀。
	history := make([]Assessment, 0, nHistory+nWrites)
	for i := 0; i < nHistory; i++ {
		history = append(history, Assessment{
			EmployeeID: emps[nSparse+int(rng.intn(nEmp-nSparse))].ID,
			Time:       testStart + rng.intn(yearSec),
			Score:      int(rng.intn(101)),
		})
	}

	s, err := New(testStart, emps, history)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runtime.GC()
	var memAfterLoad runtime.MemStats
	runtime.ReadMemStats(&memAfterLoad)

	// 交错执行写入与查询。
	start := time.Now()
	for i := 0; i < nWrites; i++ {
		var a Assessment
		if i%2 == 0 {
			// 至少一半写入：向稀疏员工年初补录并反复覆盖（少量时间点被反复写入），
			// 其下一条测评在年末或不存在，影响延续到年末。
			a = Assessment{
				EmployeeID: emps[i%nSparse].ID,
				Time:       testStart + int64((i/nSparse)%40)*secondsPerDay,
				Score:      int(rng.intn(101)),
			}
		} else {
			a = Assessment{
				EmployeeID: emps[rng.intn(nEmp)].ID,
				Time:       testStart + rng.intn(yearSec),
				Score:      int(rng.intn(101)),
			}
		}
		if err := s.AddAssessment(a); err != nil {
			t.Fatalf("AddAssessment(%+v): %v", a, err)
		}
		history = append(history, a)
		for q := 0; q < nQueries/nWrites; q++ {
			from := testStart + rng.intn(yearSec-90*secondsPerDay)
			span := (90 + rng.intn(int64(RangeDays)-89)) * secondsPerDay
			to := from + span
			if to > end {
				to = end
			}
			s.Query(from, to)
		}
	}
	elapsed := time.Since(start)
	runtime.GC()
	var memAfterRun runtime.MemStats
	runtime.ReadMemStats(&memAfterRun)

	t.Logf("interleaved %d writes + %d queries in %v (budget %v)", nWrites, nQueries, elapsed, timeBudget)
	t.Logf("HeapInuse after load = %d MiB, after run = %d MiB (budget 256 MiB)",
		memAfterLoad.HeapInuse>>20, memAfterRun.HeapInuse>>20)
	if elapsed > timeBudget {
		t.Errorf("interleaved phase took %v, budget %v", elapsed, timeBudget)
	}
	if memAfterLoad.HeapInuse > memBudget || memAfterRun.HeapInuse > memBudget {
		t.Errorf("HeapInuse %d/%d MiB exceeds 256 MiB", memAfterLoad.HeapInuse>>20, memAfterRun.HeapInuse>>20)
	}

	// 正确性抽查：长区间查询与从全部记录独立重算一致。
	tls := buildRef(emps, history)
	for _, iv := range [][2]int64{
		{testStart, end},
		{testStart + 30*secondsPerDay, testStart + 300*secondsPerDay},
	} {
		checkQuery(t, s, tls, iv[0], iv[1])
	}
}
