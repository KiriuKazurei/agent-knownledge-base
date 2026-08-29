# Skill 外部 Agent/项目映射计划

> 状态：后端、桌面 API 与 React 工作台已实现；真实外部 Agent 目录的手工验收待执行
> 适用范围：Windows 10/11 x64、本地桌面会话、全局 Skill 管理

## 1. 目标与现状

现有全局 Skill 由知识库统一保存于：

```text
<dataRoot>/skills/<skill-name>/SKILL.md
```

本功能允许电脑中的其他 Agent 或项目直接使用这些 Skill，而无需复制 Skill 内容。用户指定一个实际的 Skills 目录，系统在该目录下创建目录符号链接：

```text
<target-skills-directory>/<skill-name>
    -> <dataRoot>/skills/<skill-name>
```

Skill 内容仍由知识库统一维护、替换、备份和只读分发；外部 Agent 通过其原生 Skills 目录发现软链接后的标准 `SKILL.md`。

## 2. 映射模型

### 2.1 映射目标

一个映射目标代表一个外部 Agent 或项目的实际 Skills 目录，字段包括：

- `id`：UUID。
- `name`：用户可读名称。
- `kind`：`agent` 或 `project`。
- `directoryPath`：目标 Skills 目录的规范化绝对路径。
- `status`：目标总体状态。
- `error`：最近一次校验或操作错误。
- `createdAt`、`updatedAt`、`lastVerifiedAt`。

V1 由用户直接选择 Skills 目录，不自动猜测 Agent 目录约定，也不要求识别具体 Agent 产品。

### 2.2 映射项

一个映射项连接一个目标和一个全局 Skill，字段包括：

- `targetId`、`skillId`。
- `linkName`：默认使用 Skill 的规范名称。
- `linkPath`：目标目录下的链接相对路径。
- `status`：`ready`、`missing`、`conflict`、`permission_required` 或 `invalid`。
- `error`、`lastVerifiedAt`、`createdAt`、`updatedAt`。

目标与 Skill 是多对多关系。`linkPath` 使用 Skill 名称生成，不允许用户自定义别名，避免不同 Agent 看到不一致的能力名称。

## 3. 文件系统与安全边界

- 仅创建 Windows 真实目录符号链接，不回退为 junction、硬链接或文件复制。
- 目标目录必须已存在且为目录；系统不替用户在任意路径递归创建目录。
- 目标路径必须是绝对路径，并经过 `Clean`、规范化和目录检查。
- 拒绝目标位于 `dataRoot/skills` 内或其子目录内，避免在 Skill 源目录中形成自引用结构。
- 创建前检查目标项：
  - 不存在：允许创建；
  - 已是指向对应全局 Skill 的目录软链接：幂等成功；
  - 普通文件、普通目录、指向其他位置的软链接或无法验证的重解析点：返回冲突，不覆盖。
- 后端使用 `os.Lstat`/`os.Readlink` 等链接本身检查，不跟随外部链接删除或覆盖未知内容。
- 创建软链接所需权限不足时返回稳定错误码，并提示启用 Windows 开发者模式或以管理员身份运行。
- 映射接口只允许桌面会话访问；Agent Token 不能创建、修改、修复或删除映射。
- 所有创建、验证、修复、解除、遗忘和删除操作写入审计日志，包含请求 ID、目标 ID、Skill ID、结果和错误码。

### 3.1 批量创建与回滚

创建目标并批量映射 Skill 时先对全部 Skill 和目标路径进行预检。实际创建按项执行；任一项失败时，只删除本次操作已经创建且仍能确认指向对应源目录的链接，并回滚数据库记录，避免留下半成品映射。

## 4. 生命周期规则

### 创建、验证与修复

- 创建时保存目标和映射项，并立即校验链接。
- “验证”只读取文件系统并更新状态，不创建或删除链接。
- “修复”只处理 `missing` 项；如果目标位置存在任何未知对象，仍返回 `conflict`，不覆盖。
- 软链接目标目录被 Skill 替换时保持 `dataRoot/skills/<skill-name>` 路径不变，已有链接继续有效。

### 解除映射与遗忘

- 解除映射只删除仍指向对应全局 Skill 的软链接。
- 链接已经被外部修改时不删除目标对象，保留 `conflict` 状态，并提供“遗忘记录”操作。
- 目标目录不存在时可以删除数据库中的缺失映射记录，因为没有外部对象需要处理。
- 删除映射目标前，逐项执行同样的安全检查；无法安全删除的外部对象不得被系统触碰。

### Skill 删除与备份恢复

- 只要 Skill 仍有映射记录，删除 Skill 返回冲突；用户必须先解除或遗忘全部映射。
- 备份包含映射目标和映射项的 SQLite 元数据，但不打包外部目录，也不复制外部软链接。
- 备份恢复后，外部路径标记为未验证；系统不自动写入外部目录，用户必须显式执行验证或修复。

## 5. 数据库设计

新增表：

```sql
skill_mapping_targets(
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  kind TEXT NOT NULL CHECK(kind IN ('agent', 'project')),
  directory_path TEXT NOT NULL UNIQUE,
  status TEXT NOT NULL,
  error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  last_verified_at TEXT
)

skill_mappings(
  target_id TEXT NOT NULL REFERENCES skill_mapping_targets(id) ON DELETE CASCADE,
  skill_id TEXT NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
  link_name TEXT NOT NULL,
  link_path TEXT NOT NULL,
  status TEXT NOT NULL,
  error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  last_verified_at TEXT,
  PRIMARY KEY(target_id, skill_id)
)
```

`directory_path` 是外部机器路径，允许保存绝对路径；它不改变现有知识文件和 Skill 根目录使用相对 `dataRoot` 路径的规则。

## 6. 桌面 API

以下接口全部位于 `/api/v1`，沿用桌面认证、请求 ID、Problem Details 错误结构和审计机制：

- `GET /skill-mapping-targets`：返回目标、映射项和状态。
- `POST /skill-mapping-targets`：创建目标并批量映射。请求包含 `name`、`kind`、`directoryPath`、`skillIds`。
- `PATCH /skill-mapping-targets/{id}`：修改目标名称和类型，不允许静默修改目录路径。
- `POST /skill-mapping-targets/{id}/skills`：追加一组 Skill 映射。
- `DELETE /skill-mapping-targets/{id}/skills/{skillId}`：解除单项映射。
- `DELETE /skill-mapping-targets/{id}/skills/{skillId}/record`：只遗忘映射记录，不触碰外部冲突对象。
- `POST /skill-mapping-targets/{id}/verify`：重新检查目标和全部链接。
- `POST /skill-mapping-targets/{id}/skills/{skillId}/repair`：修复单个缺失链接。
- `DELETE /skill-mapping-targets/{id}`：删除目标记录，并按安全规则清理可确认归属的链接。

建议稳定错误码包括：

- `skill_mapping_target_invalid`
- `skill_mapping_target_not_found`
- `skill_mapping_path_not_absolute`
- `skill_mapping_source_nested`
- `skill_mapping_conflict`
- `skill_mapping_permission_required`
- `skill_mapping_link_invalid`
- `skill_mapping_skill_not_found`
- `skill_mapping_skill_in_use`

## 7. Electron 与桌面端界面

### 7.1 IPC 边界

Electron 主进程新增目录选择器 IPC，返回用户选择的既有目录。渲染器只提交路径和 Skill ID；软链接创建、验证、修复和删除全部由受保护的 Go API 执行，避免向渲染器暴露任意文件系统写权限。

### 7.2 Skills 工作台

在现有 Skills 工作台中增加“外部映射”二级视图：

- 目标列表显示名称、Agent/项目类型、目录、映射数量、状态和最近校验时间。
- 目标详情显示已映射 Skill、链接路径、源路径和状态。
- 创建表单包含名称、类型、目录选择、Skill 多选和预检结果。
- 提供验证、修复、打开目标目录、解除映射和遗忘记录操作。
- 删除和遗忘属于危险操作，单独展示并要求确认；普通验证和打开目录不使用确认弹窗。
- 所有表单字段有可见标签；路径冲突、权限错误和外部变更在对应字段或行内展示，并给出恢复方式。
- 创建失败后保留已输入内容，焦点移动到错误摘要或第一个无效字段；状态通过 `aria-live` 反馈。
- 继续使用现有深色/浅色主题、SVG 图标、键盘导航、焦点样式和减少动画支持。

## 8. 实施顺序

1. 增加 model、SQLite migration、映射状态和错误码。已完成。
2. 实现路径验证、软链接创建/读取/删除和批量回滚。已完成。
3. 实现桌面 API、审计记录、OpenAPI 契约和删除 Skill 的映射保护。已完成。
4. 增加 Electron 目录选择 IPC 和 React API 类型。已完成。
5. 在 Skills 工作台加入外部映射视图和错误/加载/空状态。已完成。
6. 增加自动化测试，再进行真实外部 Skills 目录手工验收。自动化测试已完成；真实外部目录手工验收待执行。

## 9. 测试与验收

### Go 与 API

- 创建真实软链接并通过 `Readlink` 验证目标。
- 重复创建同一正确链接为幂等成功。
- 拒绝相对路径、路径穿越、源目录内嵌套目标、非目录目标和权限不足。
- 拒绝覆盖同名文件、目录或外部链接。
- 验证缺失链接修复、外部变更保护、批量失败回滚和审计记录。
- 验证被映射 Skill 无法删除，解除映射后可删除。
- 验证桌面接口权限、错误码、请求 ID、映射 CRUD 和备份元数据。

### Electron、React 与手工验收

- 目录选择器只返回目录，取消操作不改变表单。
- 映射目标列表、详情、状态、验证、修复、解除和错误反馈可用。
- 键盘可完成创建、选择、确认和返回；错误不会丢失输入或焦点。
- 在临时外部 Agent/项目 Skills 目录中确认其他 Agent 能直接读取 `SKILL.md`、`references`、`scripts` 和 `assets`。
- 替换知识库中的 Skill 后，外部软链接仍能读取新内容。
- 备份恢复后不自动修改外部目录，显式验证后才能恢复为可用状态。

## 10. 非目标与默认假设

- V1 不自动识别 Codex、Claude 或其他 Agent 的目录约定。
- V1 不复制 Skill 内容，不执行 Skill 脚本，也不通过 Agent API 创建链接。
- V1 只支持 Windows 目录符号链接；跨平台链接策略另行规划。
- 映射目标路径由用户授权选择，系统不会扫描或枚举用户未选择的目录。
