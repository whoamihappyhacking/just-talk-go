# Omarchy Quickshell 集成

把 Just Talk 的录音状态显示在 Omarchy 顶栏里，替代内置的悬浮胶囊。

录音中显示录音点和已录时长，连接、转写、等待重试和错误各有自己的字形，空闲时整个 widget 从顶栏消失。颜色跟随当前 Omarchy 主题，不需要单独配色。

## 安装

需要 Omarchy 4.0 或更新版本（带 shell 插件系统）。

```bash
ln -sfn "$PWD/integrations/omarchy/justtalk.voice" ~/.config/omarchy/plugins/justtalk.voice
omarchy plugin enable justtalk.voice
```

然后在 `~/.config/just-talk/config.toml` 里打开状态输出，并关掉悬浮胶囊：

```toml
[overlay]
  enabled = false

[status_file]
  enabled = true
  path = ""        # 留空表示 $XDG_RUNTIME_DIR/just-talk/status.json
```

重启 Just Talk 即可。widget 的位置用 _Setup > Plugins_ 或直接编辑 `~/.config/omarchy/shell.json` 的 `bar.layout` 调整。

## 可选设置

写在 `shell.json` 里该 widget 的 layout 条目上：

| 设置 | 默认值 | 说明 |
|---|---|---|
| `idleVisible` | `false` | 空闲时也保留在顶栏 |
| `showElapsed` | `true` | 录音时显示已录时长 |
| `statusPath` | 跟随 `XDG_RUNTIME_DIR` | 状态文件路径，需与 `status_file.path` 一致 |

```json
{ "id": "justtalk.voice", "idleVisible": true, "showElapsed": false }
```

## 状态文件格式

其他状态栏（waybar、eww、polybar）也可以直接读这个文件。字段名是对外契约。

```json
{
  "state": "recording",
  "detail": "正在录音",
  "recording": true,
  "started_at": "2026-10-19T15:47:54.000000000+08:00",
  "pending_finishes": 0,
  "updated_at": "2026-10-19T15:47:58.000000000+08:00",
  "pid": 97480
}
```

`state` 取值：`idle`、`recording`、`connecting`、`stopping_delayed`、`stopping`、`waiting_retry`、`error`。

`started_at` 只在录音时出现，这样 widget 可以自己跑计时器，而不需要 Just Talk 每秒重写文件。文件只在状态变化时重写，并且是原地截断写入（inode 不变），inotify 监听不会断。Just Talk 退出时会删掉这个文件，所以「文件不存在」表示没在运行，而不是空闲。
