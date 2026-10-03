package risk

import "sort"

const (
	blockSize    = 1024 // 目标块大小（段数）
	maxBlockSize = 2048 // 超过则分裂
)

// tlElem 是 S(t) 的一个分段常数区间 [start, start+dur)。
type tlElem struct {
	start int64
	dur   int64
	val   int64 // 段内 S 值，不含所属块的懒标记
}

// valDur 用于块内按值排序的副本。
type valDur struct {
	val int64
	dur int64
}

// tlBlock 是连续若干段的集合。块内所有段共享一个懒标记：
// 段的实际 S 值 = val + lazy。
type tlBlock struct {
	elems   []tlElem
	lazy    int64
	sumVD   int64    // Σ val*dur，不含懒标记
	dur     int64    // Σ dur
	sorted  []valDur // elems 按 val 升序的副本
	prefDur []int64  // sorted 的 dur 前缀和
}

// timeline 维护部门加权总分 S(t) 在统计范围内的分段常数函数。
// 采用按段数的平方分解：整块的区间加只累加懒标记，
// 低于阈值的时长在块内排序副本上二分得到。
type timeline struct {
	blocks    []*tlBlock
	threshold int64 // 高风险阈值：S < threshold
}

// build 由按时间排序、首尾相接的分段构造时间轴。
func (tl *timeline) build(elems []tlElem, threshold int64) {
	tl.threshold = threshold
	tl.blocks = nil
	for i := 0; i < len(elems); i += blockSize {
		j := i + blockSize
		if j > len(elems) {
			j = len(elems)
		}
		b := &tlBlock{elems: elems[i:j]}
		b.rebuild()
		tl.blocks = append(tl.blocks, b)
	}
}

func (b *tlBlock) rebuild() {
	b.sumVD = 0
	b.dur = 0
	if cap(b.sorted) < len(b.elems) {
		b.sorted = make([]valDur, len(b.elems))
	} else {
		b.sorted = b.sorted[:len(b.elems)]
	}
	for i, e := range b.elems {
		b.sumVD += e.val * e.dur
		b.dur += e.dur
		b.sorted[i] = valDur{val: e.val, dur: e.dur}
	}
	sort.Slice(b.sorted, func(i, j int) bool { return b.sorted[i].val < b.sorted[j].val })
	if cap(b.prefDur) < len(b.sorted) {
		b.prefDur = make([]int64, len(b.sorted))
	} else {
		b.prefDur = b.prefDur[:len(b.sorted)]
	}
	var acc int64
	for i, vd := range b.sorted {
		acc += vd.dur
		b.prefDur[i] = acc
	}
}

func (b *tlBlock) first() int64 { return b.elems[0].start }

func (b *tlBlock) end() int64 {
	last := b.elems[len(b.elems)-1]
	return last.start + last.dur
}

// countBelow 返回块内实际 S 值严格小于 thr 的累计秒数。
func (b *tlBlock) countBelow(thr int64) int64 {
	// 第一个 val+lazy >= thr 的位置之前的 dur 前缀和。
	idx := sort.Search(len(b.sorted), func(i int) bool {
		return b.sorted[i].val+b.lazy >= thr
	})
	if idx == 0 {
		return 0
	}
	return b.prefDur[idx-1]
}

// locate 返回覆盖时刻 t 的 (块下标, 块内段下标)。t 必须在时间轴范围内。
func (tl *timeline) locate(t int64) (int, int) {
	bi := sort.Search(len(tl.blocks), func(i int) bool {
		return tl.blocks[i].first() > t
	}) - 1
	b := tl.blocks[bi]
	ei := sort.Search(len(b.elems), func(i int) bool {
		return b.elems[i].start > t
	}) - 1
	return bi, ei
}

// splitAt 保证 t 是某个段的起点（若 t 落在段内部则切开该段）。
// t 等于时间轴起点或终点时为空操作。
func (tl *timeline) splitAt(t int64) {
	if t <= tl.blocks[0].first() || t >= tl.blocks[len(tl.blocks)-1].end() {
		return
	}
	bi, ei := tl.locate(t)
	b := tl.blocks[bi]
	e := b.elems[ei]
	if e.start == t {
		return
	}
	// 切开段 e：[e.start, t) 与 [t, e.start+e.dur)。
	left := tlElem{start: e.start, dur: t - e.start, val: e.val}
	right := tlElem{start: t, dur: e.start + e.dur - t, val: e.val}
	ne := make([]tlElem, 0, len(b.elems)+1)
	ne = append(ne, b.elems[:ei]...)
	ne = append(ne, left, right)
	ne = append(ne, b.elems[ei+1:]...)
	b.elems = ne
	if len(b.elems) > maxBlockSize {
		mid := len(b.elems) / 2
		nb := &tlBlock{elems: append([]tlElem(nil), b.elems[mid:]...), lazy: b.lazy}
		b.elems = b.elems[:mid]
		b.rebuild()
		nb.rebuild()
		tl.blocks = append(tl.blocks, nil)
		copy(tl.blocks[bi+2:], tl.blocks[bi+1:])
		tl.blocks[bi+1] = nb
		return
	}
	b.rebuild()
}

// rangeAdd 对 [from, to) 内的 S 值加 delta。from、to 必须已在时间轴范围内。
func (tl *timeline) rangeAdd(from, to, delta int64) {
	if from >= to || delta == 0 {
		return
	}
	tl.splitAt(from)
	tl.splitAt(to)
	for _, b := range tl.blocks {
		if b.end() <= from || b.first() >= to {
			continue
		}
		if b.first() >= from && b.end() <= to {
			b.lazy += delta
			continue
		}
		// 部分覆盖：直接修改段值并重建块。
		for i := range b.elems {
			e := &b.elems[i]
			if e.start >= from && e.start+e.dur <= to {
				e.val += delta
			}
		}
		b.rebuild()
	}
}

// query 返回 [from, to) 内 S 对时间的积分与 S 低于阈值的累计秒数。
func (tl *timeline) query(from, to int64) (integral, riskSeconds int64) {
	if from >= to {
		return 0, 0
	}
	for _, b := range tl.blocks {
		if b.end() <= from || b.first() >= to {
			continue
		}
		if b.first() >= from && b.end() <= to {
			integral += b.sumVD + b.lazy*b.dur
			riskSeconds += b.countBelow(tl.threshold)
			continue
		}
		for _, e := range b.elems {
			s := e.start
			en := e.start + e.dur
			if s < from {
				s = from
			}
			if en > to {
				en = to
			}
			if s >= en {
				continue
			}
			d := en - s
			v := e.val + b.lazy
			integral += v * d
			if v < tl.threshold {
				riskSeconds += d
			}
		}
	}
	return integral, riskSeconds
}
