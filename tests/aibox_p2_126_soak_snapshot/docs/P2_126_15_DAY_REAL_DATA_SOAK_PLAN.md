# P2 #126｜Vela 15 天真实资料、自主测试、修复与学习验收规范（设计修订 v2）

**状态：设计修订待独立 review；运行仍是 preflight，不代表 Gate 0 PASS。** 此文件属于 Issue #126 / PR #127，不改变 0.8 Accepted Requirement 与 System Spec；发现矛盾先在 #126 reconciliation。机读最低测试矩阵见 `deploy/aibox-0.8/vela-uat/soak/cases.json`，问题类型→靶向用例与测试扩展规则见 `deploy/aibox-0.8/vela-uat/soak/learning_rules.json`。长测不能由该文档、计时器、健康检查、CI PASS 或聊天回复自动启动。

## 0. 目标 / 权限 / 唯一判卷标准

本次要证明：**Vela 上运行的真实 P2 三入口**在连续 15×24h 中，面对公开真实资料、内容变化、重复、异常、恢复和正常资源压力，仍能保证 Raw Once、稳定身份/版本、知识与时间事实正确、可追溯证据、不误删、不丢失，且发生 Bug 后能自主完成受控修复和增量测试。

- 唯一主线 `tevriqorg/P2-AIBOX#126`；现有 WeKnora PR #6 / P2 PR #127 为实现路径，不另造并行方案。Vela **当前**承担 UAT/试运行，成熟后才评估迁移，绝不等同家庭/邮件/正式数据迁移。
- Authority 依次为 C route、P2 AGENTS/current Requirement/System Spec、#126 和已合入 canonical code、冻结的试验 oracle。**测试不能修改 Requirement、初始 oracle、来源哈希、已有 FAIL 历史来达到 PASS。**
- 无 Hermes；不触碰真实 QNAP/Mail Hub/P16、其他 Vela 服务或生产卷；只 Tailscale 私网；GitHub Actions（公开）构建测试，VPS 不安装常驻编译器；敏感配置留 Vela 私有路径，GitHub 只记脱敏证据。
- **执行层与评估层必须分离**：执行层以现有 WeKnora API/Worker/Asynq/Graphiti 作为唯一业务主链；长测编排仅为定时提交测试用例/检查结果，不另造知识任务队列或 MCP。评估从数据库、API、文件哈希、事件日志和外部冻结 oracle 独立读取，不能让被测模型自己判定自己正确。
- 每个变更与故障都可关联：`trial_id / window_id / case_id / source_id / input_sha256 / oracle_revision / P2_SHA / WeKnora_SHA / runtime_digest / correlation_id / incident_id / PR / CI / retest_evidence`。所有 ID 和统计脱敏后可回写 #126。

## 1. 阶段、计时与防止“假长测”

`PREFLIGHT -> GATE0_CANDIDATE -> SOAK_RUNNING -> SOAK_PAUSED -> SOAK_REQUALIFY -> SOAK_RUNNING`，最终 `PASS / FAIL / BLOCKED`；状态以**签名/校验的版本化 checkpoint + GitHub 留痕 + 私有 append-only 事件**联合证明，绝非只凭 `gate.json` 里一个时间戳。

**Gate 0 必须逐项真实通过才能设置 UTC t0**：

1. P2 与 WeKnora 的对应 branch→PR→公开 Actions build/test/review→merge，Vela 已部署 exact 可校验 artifact；原数据卷 ID/备份/回滚版、路由/私网限制、资源配额与基线采样记录齐全。
2. 同一实际运行 WeKnora 的 KB 内，At-rest（external no-copy）、Transit（stage→verify→analyse→atomic commit→持久化 Resource/Knowledge/SourceRevision→有效 handoff receipt→cleanup）、Continuous（真实公开 feed 的 cursor backfill/incremental/update/withdraw）都有真正 E2E 证据，不能用模拟 Go service unit tests 或 URL 200 代替。
3. 身份/版本/引用：三入口重复导入、同源新版、回放；stable KnowledgeID、SourceRevision、归档原件唯一且哈希一致、正确 page/timecode locator、Temporal current/valid_at/known_at/history，以及权限拒绝/Graphiti fail-closed 均实测。
4. 冻结**独立 oracle**与初始 15 天 case matrix、每小时/每日任务调度、资源保护与阈值、通知送达测试、自动修复控制面、可重现的隔离故障注入、备份恢复 dry-run、缺测检测；每项有机器可核对的 PASS artifact。
5. t0 必须由验收任务检查 GitHub checkpoint 和现场证据后生成，持久化 `trial_id`、时区 UTC、组件 hash、样本集版本、冻结 oracle hash、矩阵 hash、回滚 snapshot ID。Gate 缺字段、修改时间戳、时间倒退或部署 digest 不符 = `BLOCKED`，不得累计时长。

- 以 UTC 连续 360 个**不重叠的 60 分钟 slot**计，不用“15 条日报”。每个 slot 至少一个完整健康/关键业务轻探针；缺 slot 不允许事后补造样本。调度延迟容忍有明确 `scheduled_at / actual_at / lag`，超过 60 分钟无真实探针记 coverage fail。计数从 t0 连续算，不因休息日暂停。
- 每天有至少两种不同格式的真实数据业务验证、一个幂等 replay 和一个来源变更/增量检验；人工冻结 oracle 或已评审的独立 reference 作为判定。每日应满足独立查询与引用断言，保存输入→输出→证据最小复现实验。
- `P0` 数据破坏/越权/误删、`P1` 核心身份/Raw Once/证据/时间正确性失败立即停止该试验、保护数据，并 **requalify 后新 trial_id 从 t0 重新计 15 天**。可恢复短时 P2/P3 外部故障经根因与回归审计允许继续，但失去 slot 或必跑案例仍须重置窗口；不能靠“暂停计时但保留累计”假装连续。
- 修复中允许后台保持**只读安全健康 preflight**。老 trial 失败保留历史；新版本或影响数据语义的修复需从 t0 开始；非语义热修是否重置由冻结的变更分类规则判定并记录，不能由修复作者私自豁免。
- 已超过 360h 不自动 PASS：必须再通过全部必测矩阵、每日证据、时段覆盖、最后一次完整回归/恢复与独立 review；若证据不足为 BLOCKED。

## 2. 测试层次与调度（不是只有健康检查）

| 层 | 频率 / 执行位置 | 必须验证 |
|---|---|---|
| L0 只读探针 | 每小时、现有 systemd timer | Web/API/Graphiti、Worker、数据库/Redis、磁盘/内存、服务重启、关键错误；保持不改业务数据 |
| L1 轻量业务探针 | 每小时，独立测试 KB、小样本 | 真正读取固定 Knowledge、检索命中冻结原句与 SourceLocator；一次有界重放/幂等，确认队列最终完成而非仅 HTTP 200 |
| L2 完整三入口回归 | 每日（错峰），隔离测试 KB + 专属测试目录 | 两种真实格式以上、At-rest/Transit/Continuous 轮换、SHA/Raw count/identity/revision/cursor、问答 citations 和 temporal as-of |
| L3 变更/异常注入 | D4-D15 分阶段，在可销毁 clone/fixture | 重复/并发/断网/失败 ack/kill/重启/资源低水位/Graphiti down/up/DB 迁移/restore，必须恢复并与原 oracle 一致 |
| L4 独立审查 | 日报+每 3 天审查+最后 360h 后 | failure ledger、oracle 漂移、覆盖缺口、Bug 修复有效性、资源趋势、出厂/迁移前的完整证明 |

- 具体必须执行用例、所属入口和严重度见 `cases.json`。表中的「待实现」不能借文档判为实现：Work 要把每个 case 映射到可执行脚本、最迟日期、CI 与可获取的证据路径。**无脚本或持续未执行的必测项 = BLOCKED。**
- 双因素覆盖：**来源类型**（文档/图片/视频/音频/持续 feed）× **生命周期**（首次/重复/更新/撤回/恢复）× **结果**（KB/Raw/SourceRevision/Temporal/引用）；使用风险优先 pairwise 组合，重要 P0/P1 必测全覆盖，不能仅同一个 fixture 跑 15 次。
- L0/1 只在现有 UAT 上做低影响、有限频率验证，最大重试/并发有上限；L3 仅对**克隆数据卷、独立私有网络、独立 test account** 故障注入。不得在现有持久卷做删除、重置、暴力压力或高风险权限测试。
- 控制 8GiB RAM/50GiB 根盘：默认业务并发≤2，下载/样本按计划有单项上限并记录带宽，CPU/内存/磁盘超软阈自动减载暂停新采样；低于 5GiB 可用磁盘或 1GiB 可用内存（本 UAT 自定义、非国际标准）停止新入库，**先保护已接收 Raw，绝不自动删除已提交 Raw 或数据库**。记录 p50/p95 端到端延迟、队列深度、失败/重试率、CPU/内存/磁盘增长、写放大和成本。
- 取样 schedule & telemetry 要分开；若探针进程死掉也应有独立外部/上级 watchdog 检出，不能由死掉的脚本报告自己健康。日报产生与报告送达都需真实测试，失败单独跟踪。

## 3. 真实公开资料与独立 oracle

原始清单 `evidence/P2_126_REAL_CORPUS_MANIFEST.json` 已有 RFC 9110 TXT、RFC 9111 PDF、NASA JPEG/MP4，20 秒 clip 的 semantic windows 尚待**人工独立逐帧 oracle 验收**。继续补至少一个合法公开音频/转录、一个**真实变化过且可合法抓取的公开 RSS/Atom/GitHub release**、版本差异实物和异常输入；不能用虚构的“昨天发生变化”冒充真实外部增量。

1. 每项先归档来源 URL、下载 UTC、SHA-256/size/MIME、上游版权/许可与使用边界（只内部测试，不再公开原始字节），稳定 source ID、初始/更新时间戳、expected locator。发现许可不明先隔离不入测。
2. 人类或独立工具**在导入前**标注文本确切章节/页码、图像场景可见对象、音频可听段落/时间码、MP4 时间事件（包含明确的「不应该推断」负例），生成只追加版本的 `oracle_vN`，哈希锁定。被测模型不允许生成/修改最终裁判；发现原 oracle 错误应保留旧版本、另走审查和新 trial，不覆盖记录。
3. 来源更新真实事件只接受可验证的 provider ID/ETag/commit/version/updated_at；当天没有更新也记录`no_change` 并用**标注为 fixture**的 mutation 另测解析逻辑，不能假报真实源变更。
4. 验证至少包含 Exact match（SHA/ID/count/status）、可解释语义（召回/问答证据 locator）、时间知识事实（valid/known/current/history）和明确失败拒绝（hallucinated events、来源消失≠事实失效、越权访问）。
5. 当前存在两套 RFC KnowledgeBase ID 的历史记录：Gate 0 必须先核对/消歧，冻结 `KB ID → KnowledgeID → Resource Ref → Source SHA` 映射，否则不得拿跨 KB 的重复当 Raw Once 证据。
6. 样本规模采取**真实格式的分层采样**，不是为了压测硬拉大文件；首选现有约 19MiB 素材，其后扩展有许可的多格式真实样本，记录大小分布并在隔离 clone 做有界大文件/多并发压力，不因加入单项 100GB benchmark 破坏 Vela 资源边界。

## 4. Bug 发现→修复→靶向再测→自我学习（硬闭环）

**状态机：** `DETECTED → TRIAGED → REPRODUCED → FIX_IN_PR → CI_VERIFIED → CANARY_DEPLOYED → TARGETED_PASS → REGRESSION_PASS → LEARNING_COMMITTED → VERIFIED_CLOSED`；另有 `BLOCKED / REOPENED / QUARANTINED`。状态只能前进且必须有证据，失败会重新打开同一 incident；禁止仅改报告就关闭 Bug。

- **DETECTED**：自动收集 case_id、错误类型、SHA、环境版本、输入/输出摘要、日志窗口 correlation IDs、首/末出现时刻、频率、影响 Source/KB、队列与资源；敏感信息脱敏后写 #126 checkpoint / GitHub Bug Issue（同因多次聚类到一个 Issue，避免海量子 Issue）。原始证据不可被修复流程擦除。
- **TRIAGED**：P0 = 隐私/权限破坏、丢 Raw、误删原件、跨租户；P1 = 知识 identity/revision/receipt/time truth 错误、不可恢复入库；P2 = 有界可恢复导入/检索异常；P3 = 性能/非关键可观测性。未知关键性按更高严重度，明确该类是否导致 reset t0。
- **REPRODUCED**：先在安全 clone 使用原 SHA/oracle 与最小触发条件复现，额外生成 negative twin（权限、路径、顺序、并发、网络）排除环境误报；若尚无法复现，不得标记已修复，保留 `UNREPRODUCED`/BLOCKED 与 trace。
- **FIX_IN_PR**：优先复用上游已有能力，修改拥有问题的仓库/分支、写具体根因/修复方案，禁止更改原件/ground truth、隐式补成功状态或掩盖错误；明确数据库迁移和回滚相容性。P2→P2 PR，WeKnora→WeKnora PR。GitHub 公开 Actions build/tests + review，不在 Vela 编译。
- **TARGETED_PASS**：最小复现 + 语义相邻矩阵（同源更新、重复/并发、断网/重启、权限、不同 MIME/边界、跨 KB）都跑，必须原 Bug 红→绿且未经降低断言；至少加一个失败注入、一个恢复测试和一个由该根因触发的反例。
- **REGRESSION_PASS**：完整受影响域回归 + G0 基线 + 三入口关键用例 + temporal/权限不变式；独立评估者核对 frozen oracle 与原 Bug evidence、部署 exact digest、SourceRevision identity。不通过即 REOPENED 并回到修复。
- **LEARNING_COMMITTED**：将事故分类为 `root_cause_signature`（如 race、wrong locator、idempotency、parser、permissions、temporal、outage）；固定为可版本追踪的 `tests/regressions/INCIDENT-ID`、最小 fixture、expected assertion 与 CI gate；在 #126 报告复现方式、修复 PR、负例、风险领域权重更新。
- **VERIFIED_CLOSED**：targeted+broader+现场 canary 同时 PASS、review 已批准、关联 issue/checkpoint 完整才允许关闭。即使 Bug 修好，历史失败/重启计时记录不能抹掉。
- **自主发现新测试**：每晚以当天失败模式、遗漏的 source×lifecycle 组合、边界条件和近期 PR 触及文件，生成候选`case_proposal`；先做去重/风险/隐私/预算评估。**允许自动增加沙箱安全测试和相应 CI**，但不允许未经 review 自动修改锁定 oracle、减少断言、降低阈值、扩大网络访问或执行有破坏性的真实 UAT 故障注入。风险与未覆盖项优先排序，每日记录 `new_cases / promoted_regressions / escaped_bugs / coverage_delta`。
- **学习验证**：周期性执行 mutation/negative assertions（故意错误的 SHA、错误的 locator、伪 receipt、跨租户 token、假时间事实）证明测试确实能报红；如果测试失明，即使业务表面 PASS 也判 BLOCKED。反复出现同类 bug，提升该类回归频率，不因测试数量增加而减少独立样本来源多样性。
- 修复权限说明：已经授权按既有需求修 Bug 与测试，但需要新增生产权限、真实邮箱/家庭资源、扩大公网或高风险操作时停止并取得另行授权；安全边界优先于“自动修到绿”。

### 4.1 真正可持续的“自动执行 + 自动修复”控制链

不能假设某一次交互式 Work 会话会连续在后台运行 15 天。长测真正启动前，必须在现有基础设施中证明确实存在**可重复唤醒**的执行者、负责修复的 Agent/Work 接管机制与独立监督者：

```text
Vela systemd 定时器（只触发，不代替 WeKnora 的业务队列）
  ├─ L0 每小时只读监控（已存在）
  ├─ L1 每小时轻量真实业务测试（待实现）
  └─ L2 每日真实数据案例 + oracle 验证（待实现）
       ↓ append-only evidence / classifier
       ├─ PASS → 继续，生成每日覆盖率和新增案例候选
       └─ FAIL → fail-closed + 创建/更新 #126 incident/checkpoint + 明确告警
                    ↓ 可持续任务接力（Agent/Work/人工 fallback，有身份与权限）
                  定位/复现 → 修复 PR → GitHub Actions → review
                    ↓ exact artifact canary deploy + targeted/broad regression
                  回归通过 → 回写 incident → 固化新测试 → 更新选择权重
```

- **业务测试调度器**必须在 Vela 上以受限账户、独立测试 KB/目录、执行互斥锁和有限并发运行；单次执行上限、超时、下一次恢复/重放策略、触发时刻和持久化 case_run_id 都要写入 checkpoint。任务处理本身走现有 WeKnora/Asynq，调度器只负责发请求、校验和记结果。不可新增另一套通用任务队列。
- **Agent 自主修复通道**必须有明确的身份、最小 GitHub/Vela 权限、掉线重试、领取/释放租约、无回应升级路径。不能用“Work 以后会处理”替代部署；无人领取的 Bug 在 SLA 到期后应报警并 BLOCKED。最少验证一次非破坏性的**种子 Bug 演练**：测试必须报红→生成 incident→归因→沙箱修复→CI→针对性复测→回写覆盖增长；不能用假日志冒充演练成功。
- **通知通道**实际送达与确认是 Gate 0 项：P0/P1 检测立即报警，测试级别的建议值为 5 分钟内送达、30 分钟内有接手确认；P2 24 小时内有明确处置，P3 可进日报。这些是本轮 UAT 的运营目标，实际路径和权限未验收前一律不能宣称达标。通知失败与修复者不可用也是独立 incident。
- **初始矩阵冻结**：#126 基线 35 项的文件 SHA-256 固定为 `c3b3d89b96f1008e171831780b447bbc8d56e1f218d4651236ad7b9fb962b2e1`，静态设计门禁校验该值。动态学习后新增的永久回归独立追加为 reviewed Git 文件与候选账本，不覆盖、缩减既有 35 项基线；若原始 oracle/矩阵真实错误，只能另开版本化 rebaseline+独立复核+新 trial，不允许原地改 hash 就保持长测“连续”。
- **不可自己修改测试标准**：任何动态发现的新测试只能追加到候选集，原矩阵及 frozen oracle 是评审版本、不可自动覆盖；模型可以建议复现、生成测试代码和修复 PR，但不能自动减弱/删除 assertion、偷偷 suppress flaky 结果、改数据、跳过 review 或改动 production boundary。
- **运行成本熔断**：AI/VLM 调用、GitHub Actions 分钟、视频抽帧量、磁盘写入和网络下载设每日预算/熔断，超限可暂停高成本探索，不可牺牲 P0/P1 必测覆盖或伪造通过；触发后有 incident 与 next action。

## 5. 故障、恢复、验收与防漂移规则

| 事件 | 直接行为 | 时钟 / 后续动作 |
|---|---|---|
| P0 安全、Raw 丢失、越权 | 立刻停止相关业务写入、保存 evidence、保护 volume，不自动删除 | FAIL，修复/独立恢复后新 t0 |
| P1 误归档/错引/时态误判 | fail-closed、冻结坏案例、修复 PR 和定向回归 | FAIL/REQUALIFY，重新完整 15 天 |
| 处理超时或短网断（P2） | 幂等 retry/backoff、确认最终真实 receipt、不人工标成功 | 若 slot/必要业务用例未通过则重置，否则记录恢复时间并继续 |
| 公网测试 feed 无更新 / 官方不可达 | no_change 或外部依赖 BLOCKED，记录来源与 retry | 不得伪装成内部增量 PASS；必测缺失补真实可信场景 |
| 报警系统/采样器离线 | 独立 watchdog 报警、保留 gap，不插补 | slot coverage FAIL，必须重置或给出阻塞 verdict |
| 修复 PR 或版本升级 | 冻结 old/new SHA 与可回滚证据，clone/canary 验收 | 影响核心语义则 reset t0；非语义补丁需独立验证记录 |

复核包含：开/关 Graphiti 应返回 degraded 且不谎报 temporal truth；重复事件顺序被扰动，Knowledge ID 与 SourceRevision 仍一致；两个 parser/模型 config 版本变化不能制造“新现实事件”；FileService external ownership 永不能物理删除 source。**无确定 oracle 的语义项不得算 PASS。**

## 6. 每日审计包与最终交付判定

- **每天自动产出**：`date_utc, window_id, git SHA, image digest, source/oracle version, scheduled/executed/failed/missing slots, test_id coverage, raw_unique, KB/SourceRevision counts, cursor, latest citations, temporal verdict, duration p50/p95, retry/queue, disk/memory trend, severity incidents, fix PR/retest, new regression cases, gaps`，不包含原始私人数据/凭据。GitHub #126 更新去重汇总，运行原始事件以私有 append-only JSONL 存放，并有日志大小/保留上限、脱敏和归档哈希。
- **每三天独立审阅**：对照初始需求追踪矩阵，审核失败复现、修复审查、反例与测试增长，检查采样器自身/通知链失效；明显漂移时暂停加新功能，优先补可检验的核心路径。
- **D1–D3**：基线正确性、L0/L1/L2、文件/知识/时间与资源趋势；**D4–D7**：真实增量、修改/撤回、cursor replay、不同 MIME 与时态冲突；**D8–D11**：仅 clone 的网络/worker/Graphiti/DocReader/DB/重启/backup-restore/错误注入；**D12–D15**：渐进并发/有界大媒体、原测试及新增 regression 全量、趋势/容量/恢复与最终定格测试。每天仍要执行基线，不是互斥轮换。
- **最终独立 PASS 门槛**：①准确部署、权限隔离、可恢复备份与可回滚；②至少 360 个真实连续 hourly slots，无无解释的缺口，每日已执行约定业务与 ≥2 格式；③机读 matrix 的所有 P0/P1 必测项有 live/clone **对应层级** 的真实证据，不能靠 mock 顶替；④三入口 Raw Once、id/revision、全部强制 oracle/citation/temporal/负面安全断言 100% 通过；⑤无未解决 P0/P1，低级事件均有影响分析和修复/回归证据；⑥每个已修复 Bug 都转化成持久 regression 与新增反例；⑦初始 oracle 与 FAIL 历史未被重写；⑧最终 GitHub checkpoint 与独立 review。任何条件缺失给出 **FAIL 或 BLOCKED**，绝不“基本通过”。
- #126 在达成以上所有条件前保持 OPEN。#121 owner UAT 与迁移验收独立，不因长测自动关闭。任何 15 天未实际过去的时点只能汇报已完成的小时与已验证的功能，禁止预报 PASS。

## 7. 外部工程实践（参考而非覆盖本规范）

可复现构建、渐进 canary、自动 rollback、按变更影响度做信号监控参考 Google SRE *Canarying Releases*（https://sre.google/workbook/canarying-releases/）。NASA 官方使用政策 https://www.nasa.gov/nasa-brand-center/images-and-media/ 说明 NASA 部分媒资可能包含第三方版权、徽标/标识独立受限制；IETF Trust FAQ https://trustee.ietf.org/about/faq/ 说明 RFC 原始作者权利和 RFC 复制规则，素材许可必须逐项核查、记录在 manifest。
