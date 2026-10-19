import QtQuick
import Quickshell
import Quickshell.Io
import qs.Commons
import qs.Ui

// Reads the status file Just Talk's statusfile plugin publishes. The daemon
// removes that file on exit, so its absence means "not running" rather than
// "idle", and the widget leaves the bar entirely.
BarWidget {
  id: root
  moduleName: "justtalk.voice"

  readonly property string runtimeDir: Quickshell.env("XDG_RUNTIME_DIR") || "/tmp"
  readonly property string statusPath: setting("statusPath",
    runtimeDir + "/just-talk/status.json")

  // Shown while nothing is happening. Off by default so the bar stays quiet
  // until you actually speak.
  readonly property bool idleVisible: setting("idleVisible", false)
  readonly property bool showElapsed: setting("showElapsed", true)

  property bool running: false
  property string state: "idle"
  property string detail: ""
  property bool recording: false
  property double startedAt: 0

  // Ticks only while recording, so an idle bar does no per-second work.
  property int elapsedSeconds: 0

  readonly property bool attention: state === "error" || state === "waiting_retry"

  readonly property string glyph: {
    switch (state) {
      case "recording": return "󰑊"
      case "connecting": return "󰏤"
      case "stopping":
      case "stopping_delayed": return "󰏤"
      case "waiting_retry": return "󰑓"
      case "error": return "󰍭"
      default: return "󰍬"
    }
  }

  readonly property string stateLabel: {
    switch (state) {
      case "recording": return "Recording"
      case "connecting": return "Connecting"
      case "stopping_delayed": return "Stopping"
      case "stopping": return "Transcribing"
      case "waiting_retry": return "Press R to retry"
      case "error": return "Error"
      default: return "Idle"
    }
  }

  function formatElapsed(total) {
    var minutes = Math.floor(total / 60)
    var seconds = total % 60
    return minutes + ":" + (seconds < 10 ? "0" : "") + seconds
  }

  readonly property string label: recording && showElapsed && elapsedSeconds > 0
    ? formatElapsed(elapsedSeconds) : ""

  function apply(text) {
    var parsed
    try {
      parsed = JSON.parse(text)
    } catch (e) {
      // A torn read is possible in principle; keep the last good state and
      // wait for the next change rather than blanking the widget.
      return
    }
    running = true
    state = parsed.state || "idle"
    detail = parsed.detail || ""
    recording = parsed.recording === true
    startedAt = parsed.started_at ? Date.parse(parsed.started_at) : 0
    updateElapsed()
  }

  function clear() {
    running = false
    state = "idle"
    detail = ""
    recording = false
    startedAt = 0
    elapsedSeconds = 0
  }

  function updateElapsed() {
    if (!recording || startedAt <= 0) {
      elapsedSeconds = 0
      return
    }
    elapsedSeconds = Math.max(0, Math.floor((Date.now() - startedAt) / 1000))
  }

  visible: running && (idleVisible || state !== "idle")
  implicitWidth: visible ? button.implicitWidth : 0
  implicitHeight: button.implicitHeight

  FileView {
    id: statusFile
    path: root.statusPath
    watchChanges: true
    printErrors: false
    // text() is stale inside the change signal, so both paths go through
    // reload() and land in onLoaded with fresh content.
    onFileChanged: reload()
    onLoaded: root.apply(text())
    onLoadFailed: root.clear()
  }

  // The watcher cannot follow a path that does not exist yet, so Just Talk
  // starting or exiting is picked up by this poll rather than by inotify.
  Timer {
    interval: 1000
    running: true
    repeat: true
    onTriggered: statusFile.reload()
  }

  Timer {
    interval: 500
    running: root.recording && root.showElapsed
    repeat: true
    onTriggered: root.updateElapsed()
  }

  // The glyph and the elapsed time share one slot, so neither BarIconButton
  // (icon only) nor a plain WidgetButton label (one Text at body size) fits.
  // The glyph is therefore drawn through OpticalGlyph with the same canvas
  // size and icon font the first-party icon widgets use, which is what puts
  // it on their optical centre instead of a body-text line box.
  readonly property bool showLabel: !vertical && label !== ""

  TextMetrics {
    id: labelMetrics
    font.family: button.fontFamily
    font.pixelSize: Style.font.body
    text: root.label
  }

  WidgetButton {
    id: button
    anchors.fill: parent
    bar: root.bar
    labelVisible: false
    hasVisualContent: true
    fontSize: Style.bar.iconFont
    pressable: false
    tooltipText: root.detail !== "" ? root.stateLabel + " — " + root.detail : root.stateLabel
    active: root.recording || root.attention

    // Icon-only occupies exactly one standard icon slot; the elapsed time
    // widens the slot rather than shifting the glyph off centre.
    fixedWidth: root.vertical
      ? -1
      : Style.bar.iconSlot + (root.showLabel ? labelMetrics.width + content.spacing : 0)
    fixedHeight: root.vertical ? Style.bar.iconSlot : -1

    readonly property color contentColor: button.active && button.useActiveColor
      ? button.activeColor : button.foreground

    Row {
      id: content
      anchors.centerIn: parent
      spacing: 5

      OpticalGlyph {
        anchors.verticalCenter: parent.verticalCenter
        width: Style.bar.iconCanvas
        height: Style.bar.iconCanvas
        text: root.glyph
        fontFamily: button.fontFamily
        fontSize: Style.bar.iconFont
        color: button.contentColor
      }

      Text {
        visible: root.showLabel
        anchors.verticalCenter: parent.verticalCenter
        textFormat: Text.PlainText
        text: root.label
        color: button.contentColor
        font.family: button.fontFamily
        font.pixelSize: Style.font.body
        renderType: Text.NativeRendering
      }
    }
  }
}
