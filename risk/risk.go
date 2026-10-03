// Package risk 提供单部门钓鱼识别能力分的风险趋势统计。
// 数据保存在进程内存中，由上层报表直接调用，按单线程顺序使用。
package risk

// Employee 描述一名员工的固定信息。
type Employee struct {
	ID         string // 唯一标识
	Weight     int    // 固定权重，1 至 100
	StartScore int    // 统计起点的能力分，0 至 100
}

// Assessment 描述一条测评记录。
type Assessment struct {
	EmployeeID string // 员工标识
	Time       int64  // 生效时间，整数 UTC 秒
	Score      int    // 能力分，0 至 100
}

// Stats 是一次区间查询的结果。
type Stats struct {
	// Average 是区间内按持续时间加权的部门平均能力分。
	Average float64
	// HighRiskSeconds 是区间内部门能力分严格低于 50 的累计秒数。
	HighRiskSeconds int64
}
