# 04: 用量查询与页面

**What to build:** 完整查询接口与新增「用量」页，总览卡片增强。时间粒度 时/日/近7天/月/自定义，按供应商、模型、调用方分组与筛选。

**Blocked by:** 02, 03

**Status:** resolved

- [ ] 时间视图：时（当天，按小时）/ 日（近 30 天）/ 近 7 天 / 月（近 12 个月）/ 自定义
- [ ] 自定义跨度大时自动按月聚合
- [ ] 分组与筛选：供应商、模型、调用方
- [ ] 指标齐全：requests / ok / err / input / output / total / cached / cache_write / reasoning / missing
- [ ] 按小时仅当天可选，其余时间不进小时视图
- [ ] 改名或删除后，旧历史显示写入时的名称快照
- [ ] 新增「用量」页：时间粒度切换 + 汇总条 + 分组表；总览页今日卡片
- [ ] 测试：给定时段与分组的求和、时间序列正确

## Comments

已实现。`GET /api/usage/report` 支持 `range=hour|day|7d|month|custom`、`group=model|provider|caller` 与供应商/模型/调用方筛选，返回合计、零填充时间序列与分组明细；自定义跨度超 90 天自动转月粒度。新增「用量」页：时间与分组切换、自定义区间、汇总卡片、趋势条与分组表；总览页今日卡片在 01 已落地。

名称快照随桶保存，改名或删除后旧时段仍显示当时的名字。

验证：`go test ./...` 全绿（新增 `internal/usage/query_test.go`、`internal/admin/usage_test.go`：分组、筛选、月/时粒度、区间与粒度选择、名称快照）；`web` 构建通过。
