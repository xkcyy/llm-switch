## Agent skills

### Issue tracker

Issues and specs live as markdown files under `.scratch/<feature>/`. See `docs/agents/issue-tracker.md`.

### Triage labels

Default five-role vocabulary (`needs-triage` / `needs-info` / `ready-for-agent` / `ready-for-human` / `wontfix`). See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: root `CONTEXT.md` plus `docs/adr/`. See `docs/agents/domain.md`.

### 版本与发布

版本号唯一来源是 Git tag `vX.Y.Z`，发版走 `scripts/release.ps1`，不手工改版本字符串或手工打 tag。分支名不承载版本，功能分支用 `codex/<slug>`。`dist/` 是构建产物目录，产物只走 GitHub Releases，不提交进 Git。提交信息使用 `feat|fix|docs|chore|refactor|test` 前缀，发版脚本据此推导下一个版本号。详见 `docs/design/自动更新与版本管理方案.md` §7。
