---
name: LLM Switch
description: 本机信号路由台：把供应商、模型与 Agent 接入画成可操作的路由关系
colors:
  rail-graphite: "#0f1620"
  rail-graphite-soft: "#151e2b"
  canvas: "#f4f6f9"
  surface: "#ffffff"
  surface-soft: "#f8fafc"
  ink: "#151b24"
  ink-soft: "#5e6877"
  ink-muted: "#66707f"
  line: "#e3e8ef"
  line-strong: "#cdd5df"
  signal-blue: "#2f6bff"
  signal-blue-soft: "#eef4ff"
  signal-green: "#16a34a"
  signal-green-strong: "#15803d"
  signal-green-soft: "#eefaf3"
  signal-amber: "#d97706"
  signal-amber-soft: "#fff7ed"
  signal-red: "#dc2626"
  signal-red-soft: "#fef2f2"
  signal-cyan: "#0e7490"
  signal-violet: "#7c3aed"
typography:
  body:
    fontFamily: '-apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", sans-serif'
    fontSize: "13px"
    fontWeight: 400
    lineHeight: 1.5
  title:
    fontFamily: '{typography.body.fontFamily}'
    fontSize: "20px"
    fontWeight: 650
    lineHeight: 1.3
  label:
    fontFamily: '{typography.body.fontFamily}'
    fontSize: "12px"
    fontWeight: 600
    lineHeight: 1.4
  mono:
    fontFamily: "ui-monospace, SFMono-Regular, Menlo, Consolas, Liberation Mono, monospace"
    fontSize: "12px"
    fontWeight: 400
    lineHeight: 1.6
rounded:
  sm: "6px"
  md: "8px"
  lg: "12px"
  xl: "16px"
  pill: "999px"
spacing:
  xs: "4px"
  sm: "8px"
  md: "12px"
  lg: "16px"
  xl: "20px"
  xxl: "24px"
  section: "32px"
components:
  button-primary:
    backgroundColor: "{colors.signal-blue}"
    textColor: "{colors.surface}"
    rounded: "{rounded.md}"
    padding: "0 16px"
    height: "34px"
  panel:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
    rounded: "{rounded.lg}"
    padding: "20px"
  input:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
    rounded: "{rounded.md}"
    height: "34px"
  navigation:
    backgroundColor: "{colors.rail-graphite}"
    textColor: "{colors.ink-muted}"
    rounded: "{rounded.md}"
    height: "38px"
  signal-node:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
    rounded: "{rounded.md}"
    padding: "12px 16px"
  connect-hero:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
    rounded: "{rounded.lg}"
    padding: "24px"
---

# Design System: LLM Switch

## Overview

LLM Switch 的界面是一个本机信号路由台。它不是通用后台仪表盘：左侧 Agent 端口、中间本机代理、右侧供应商节点，共同表达产品的真实机制——一个本机代理把多家供应商接到多个 Agent。

设计服务于 Operate 模式。高频用户需要快速确认状态、找到下一步并完成配置；路由关系只在能表达状态与流向时出现，其他区域保持开发者工具的克制、熟悉与高密度。

## Colors

### Primary

- **Signal Blue** `#2f6bff`：主操作、当前选择、路由焦点。整页只允许一个主操作使用它。
- **Signal Blue Soft** `#eef4ff`：当前选择、活跃路由节点、需要轻量强调的面板背景。

### Secondary

- **Signal Green** `#16a34a`、**Signal Green Strong** `#15803d` 与 **Signal Green Soft** `#eefaf3`：运行中、已接入、成功；小字号文字使用 Strong。
- **Signal Amber** `#d97706` 与 **Signal Amber Soft** `#fff7ed`：需要注意、等待重启、测试未通过但已保存。
- **Signal Red** `#dc2626` 与 **Signal Red Soft** `#fef2f2`：失败、危险操作、已停止。
- **Signal Cyan** `#0e7490` 与 **Signal Violet** `#7c3aed`：只用于用量图表序列与轨迹类型区分。

### Neutral

- **Rail Graphite** `#0f1620`：左侧导航轨、全局状态底。
- **Rail Graphite Soft** `#151e2b`：深色区域内的次级表面。
- **Canvas** `#f4f6f9`：内容画布。
- **Surface** `#ffffff`：面板、卡片、表格。
- **Surface Soft** `#f8fafc`：表头、只读条带、代码与轨迹背景。
- **Ink** `#151b24`、**Ink Soft** `#5e6877`、**Ink Muted** `#66707f`：正文、次要说明、弱化元数据。三个值在白底上都达到 WCAG AA。
- **Line** `#e3e8ef` 与 **Line Strong** `#cdd5df`：分割线与控件描边。

### Named Rules

**One Signal Rule.** Signal Blue 只用于主操作、当前选择与路由焦点；它不能同时出现在多个同等权重的操作上。

**State Not Decoration Rule.** 绿、琥珀、红只表达状态，不作为面板背景或品牌装饰大面积使用。

## Typography

### Hierarchy

- **Page Title** 20px / 650：每页唯一的主标题。
- **Section Title** 14px / 600：面板标题。
- **Body** 13px / 400：说明、表格、表单。
- **Label / Meta** 12px / 600：字段标签、状态、元数据。
- **Mono** 12px：供应商 ID、模型 ID、URL、Windows 路径、请求原文与配置片段。

### Named Rules

**Mono Means Identifier Rule.** Mono 只用于标识符、地址、路径、代码与测量值，不用于按钮、导航或装饰性“技术感”。

**One Family Rule.** 界面只用系统无衬线；中文优先。标题、正文、按钮与数据不引入展示字体。

## Layout

页面由 `page-wrap` 承载，最大宽度 1760px。1280 / 1440 / 1920 均使用流式栅格，右侧不留固定空栏。内容区默认 20px 上边距、24px 左右边距、40px 下边距。

Shell 为左侧 248px 导航轨 + 顶部 56px 全局状态条。视口小于 1120px 时导航轨折叠为 72px；小于 900px 时改为抽屉导航。总览使用 Agent → 代理 → 供应商的三列路由台；供应商与模型使用 296px 主列表 + 右侧工作区；接入页共用同一模板：Hero → 接入前检查 → 当前配置 → 高级/扩展。

面板内边距为 20px，紧凑列表为 12px，表格与无边框区域为 0。同一组内边距只允许一处定义，页面不得写魔法间距。

## Elevation & Depth

层次靠三件事：深色导航轨、白色面板与画布底色、1px 分割线。默认面板只有 `0 10px 26px -18px rgba(15,23,42,0.22)` 一处投影；禁止在 1px 描边上再叠加宽阴影。

浮层使用 Ant Design 的 Drawer / Modal / Dropdown，不自行发明层级。路由节点与状态条不使用光晕；运行状态用实心状态点表达。

## Shapes

- 控件、输入、按钮：8px 圆角。
- 面板、卡片、Hero：12px 圆角。
- 小标签、状态点、连接标记：6px 或 pill。
- 不使用大圆角气泡，不使用硬偏移阴影，不使用描边加宽阴影的“幽灵卡片”。

## Components

### Buttons

主按钮为 Signal Blue 实底，高度 34px，圆角 8px；次按钮为默认描边；危险操作只在删除、断开、停止时使用红色文字或红色确认按钮。一个区域最多一个主按钮。

### Chips

状态标签使用 Ant Design Tag 的无边框形态；状态颜色只取语义色。供应商 ID、模型 ID、协议与文件格式可以用中性 Tag。

### Cards / Containers

面板用 `panel` 组件：标题 14px、说明 12px、内边距 20px。卡片只用于真实分组，禁止卡中卡。统计信息用 `metric-strip` 横向读数带，不用等权卡片阵列。

### Inputs / Fields

表单沿用 Ant Design 5.22.x；标签在上、必填星号关闭、错误在字段下方。输入高度 34px，圆角 8px，聚焦环使用 Ant Design 默认焦点样式。长文本字段使用 mono。

### Navigation

导航分四组：概览 / 配置 / 接入 / 观测。当前项为 Signal Blue 实底白字；其余为深色轨上的浅灰文字。接入的 5 个 Agent 共用一个入口，内部用 Agent tab 切换，不把 5 项平铺到主导航。

### Signature Component

**Signal Node**：路由台上的节点，包含图标、名称、元数据与状态点。已接入节点使用 Signal Blue Soft 背景与描边；未接入节点降低不透明度；状态点使用绿/灰。

## Do's and Don'ts

### Do

- 先让用户看到当前状态与唯一下一步，再提供管理入口。
- 同类对象、同类操作、同类状态在所有页面使用同一套组件。
- 长 ID、URL、路径与配置片段用 `CopyText` 截断 + 复制。
- 每个页面覆盖加载、空、错误、成功与未就绪状态。
- 用真实术语，遵循 `CONTEXT.md`。

### Don't

- 不要用 6 张等权统计卡作为总览结构。
- 不要在页面内写硬编码色值、圆角、字号或魔法间距。
- 不要为视觉效果增加用户步骤、字段或确认。
- 不要把路由台隐喻扩散到表格与表单，那里以任务效率优先。
- 不要用渐变文字、装饰性玻璃、彩色左边框或 Emoji 充当图标。
