# 验证记录

验证日期：2026-09-22（Asia/Shanghai）

## 代码质量

以下命令均实际执行成功：

```bash
cd backend
gofmt -w <本次修改的 Go 文件>
go test ./...
go test -race ./...
go vet ./...
go build ./...

cd ../frontend
npm run typecheck
npm run build

cd ..
docker compose config --quiet
```

- 非测试 Go 代码：3239 行。
- 非测试 `.go` 文件：38 个。
- 服务层回归测试覆盖证书异人发布、放行双人复核、operator 越权、同人伪装 reviewer、复核后编辑锁定、版本与审计数量。
- `internal/service/evidence_freeze_test.go` 覆盖证据冻结：提交时冻结检查任务/证书两端版本并写入版本快照；无关联编号、检查未通过、证书无效、完全无证据四种阻断均保持草案且阻断编号刷新可回读；批准前检查任务或证书版本漂移返回 `INSPECTION_VERSION_CHANGED`/`CERTIFICATE_VERSION_CHANGED` 且记录保持 review；退回草案清除冻结、重新提交冻结新版本后双人复核批准成功；批准后证据继续变化不影响已冻结证据，已批准记录拒绝改写。

## 本地 SQLite 端到端（2026-09-22）

使用 SQLite 开发模式实际启动服务（`DATABASE_DRIVER=sqlite REDIS_ADDR=''`），通过 API 验证：

- 检查任务 planned→running→passed（v3）、证书 draft→valid（v2，reviewer 发布）。
- 无关联编号或证据不全的放行提交返回 HTTP 409 `evidence_blocked`，记录保持 draft/v1，GET 回读 `evidenceConsistent=false`、阻断编号与中文原因。
- 证据齐全的放行提交冻结 `INSP/CERT` 编号与版本（3/2），版本快照含冻结字段，列表与详情均可读。
- 提交后检查任务更新到 v4，reviewer 批准返回 HTTP 409 与 `INSPECTION_VERSION_CHANGED`（含冻结/当前版本），记录保持 review/v2，GET 回读当前 v4、冻结 v3、不一致。
- reviewer 退回 draft（冻结清除，v3）→ operator 重新提交（冻结 v4，v4）→ reviewer 批准（approved v5，双人复核成立）。
- 批准后底层检查任务再次更新，已批准记录仍保留冻结 v3，PUT 改写返回 HTTP 409。

## 空卷 Compose 与 API

执行 `KEEP_RUNNING=1 ./scripts/validate.sh`，脚本先运行 `docker compose down -v --remove-orphans`，再从空命名卷构建并启动 MySQL、Redis、MinIO、backend、frontend。验证结果：

- `/healthz` 返回 database=`ready`、redis=`ready`。
- `viewer` 可读取部件与会话，POST 写入返回 HTTP 403。
- `operator` 创建放行草稿 v1，并提交为 review v2；其自批请求返回 HTTP 403。
- `reviewer` 独立批准为 v3，`submittedBy=operator`、`reviewedBy=reviewer`。
- 三个放行版本分别保留 actor、request ID、状态、证据和原因。
- `operator` 创建证书 v1；其发布请求返回 HTTP 403；`reviewer` 发布为 valid v2。
- 证书版本保留 `preparedBy=operator`、`verifiedBy=reviewer` 和请求 ID。
- 实体审计历史包含 `gb515-auth-create`、`gb515-auth-review`、`gb515-auth-approve`；审计汇总覆盖两个独立操作者。

## 内置 Browser

仅使用 Codex 内置 Browser，在 `http://127.0.0.1:18515` 实际验证：

- 登录页可选择 admin/reviewer/operator/viewer 并建立真实 JWT 会话。
- `/parts`、`/inspections`、`/certificates`、`/authorizations`、`/audit` 五个页面均加载成功。
- 在部件页通过确认对话框将 AP-001 从 received 推进到 inspection，页面刷新后状态正确。
- `PartStatusBadge` 在部件与放行页渲染；`CertificatePanel` 在检查与证书页展示版本、操作者和请求 ID。
- operator 在 review/approved 放行记录上只看到“等待复核员”，没有批准按钮；draft 仍可提交 review。
- viewer 不显示新增和状态推进按钮，只显示只读状态。
- 审计页展示 operator/reviewer 的创建、提交、批准、发布动作及对应请求 ID。
- 桌面全页截图和 390 x 844 移动端截图已检查；移动端表格采用稳定横向滚动，不挤压文字。
- 最终控制台 `error`/`warning` 日志为空。

## 清理

验证结束后执行：

```bash
docker compose down -v --remove-orphans
```

并确认没有名称包含 `aircraft-component-airworthiness-release` 的运行中容器。
