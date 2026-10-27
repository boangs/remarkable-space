// 文档「…」菜单里「启用形状」下面加一行「刷新」三档: 高清 / 快刷 / 流畅, 只管当前这份文档,
// 按文档记住 (存在空间数据区 shell/fastrefresh.json: {"docs": {"<文档 id>": "ui" | "pen"}}; 高清 = 不记)。
// 名字和空间设置里的刷新档位一致: 快刷 = UI, 流畅 = Pen (带彩色的快刷)。2026-10-27 真机六档都试过,
// Mono / Animation / Overlay 不要 (Overlay 同 Pen, Animation 只有黑白)。
//
// 系统给文档页标的是 Content (完整灰阶, 慢、翻页闪) —— 也就是「高清」, 不用挂东西;
// 快刷 (UI) / 流畅 (Pen) 是整屏常驻一个 ScreenModeItem 盖过去。几何必须常驻不变
// (EPScreenModeMap 每变一次几何就整屏重合成): 快刷↔流畅只改 mode, 只有回高清时才拆。
// 拆之前先把档位改回 Content 再等一拍销毁: 直接销毁时屏幕驱动里那块快刷标记可能留着不清。
//
// 下面再一行「点击翻页」开关 (同样按文档记住, 存同一个文件的 "tap": {"<文档 id>": true}; 默认关):
// 手指点屏幕左 1/3 上一页、右 1/3 下一页, 中间留给系统 (呼出工具栏)。只认触屏 (笔点不算),
// 笔在感应范围内或刚离开 1 秒内不翻, 顶部工具栏那条和弹出菜单开着时不翻。
// TapHandler 是被动监听, 系统的滑动翻页 / 双指缩放照常。手掌偶尔会被当成点击 (界面层分不清手掌,
// 芯片报的 PALM 类型到 Qt 这层丢了), 2026-10-27 和用户商定先这样。
//
// 菜单行作为列表 (ColumnLayout) 里独立的项, 插在「启用形状」后面 (做法见 attachRow)。
// 运行时注入的组件里 Timer 不触发, 所有"等一会"都靠注入库每秒递增的 hookTick。
// xofm.libs.epaper / ark.controls 都不静态 import, 运行时 createQmlObject: 哪台机器 (如 rm2 的老固件)
// 缺哪个模块, 只少对应那一样 (刷新档位 / 原生开关), 整个挂件不会因为一行 import 编译失败。
import QtQuick
import QtQuick.Layouts

Item {
    id: root
    property var docView: null          // 注入库给的 DocumentView
    property var clip: null
    property int hookTick: 0
    anchors.fill: parent

    readonly property string api: "http://127.0.0.1:8080/space/apps/shell/data/fastrefresh"
    readonly property string docId: docView && docView.document ? ("" + docView.document.id) : ""
    property var docs: ({})
    property bool loaded: false
    // 当前文档的档位: content (高清, 系统默认) / ui (快刷) / pen (流畅)
    readonly property string mode: {
        if (!loaded || !docId) return "content"
        var m = docs[docId]
        if (m === true) return "ui"                     // 早期试验版存的是 true
        return (m === "ui" || m === "pen") ? m : "content"
    }

    property var taps: ({})
    readonly property bool tapOn: loaded && docId !== "" && taps[docId] === true

    property var modeItem: null         // 整屏的刷新标记
    property var dropping: null         // 已改回 Content、等下一拍销毁的旧标记
    property var menuLoader: null       // 系统菜单的「…」按钮 (secondaryToolLoader_settingsMenu), 找到就缓存
    property var row: null              // 我们插进菜单的「刷新」行
    property var tapRow: null           // 「点击翻页」行

    function log(s) { console.warn("[fastrefresh] " + s) }

    function load() {
        var x = new XMLHttpRequest()
        x.onreadystatechange = function () {
            if (x.readyState !== XMLHttpRequest.DONE) return
            var o = {}
            if (x.status === 200) { try { o = JSON.parse(x.responseText) || {} } catch (e) {} }
            root.docs = o.docs || {}
            root.taps = o.tap || {}
            root.loaded = true
        }
        x.open("GET", api)
        x.send()
    }
    function save() {
        var x = new XMLHttpRequest()
        x.onreadystatechange = function () {
            if (x.readyState === XMLHttpRequest.DONE && x.status !== 200 && x.status !== 204) log("保存失败 " + x.status)
        }
        x.open("PUT", api)
        x.setRequestHeader("Content-Type", "application/json")
        x.send(JSON.stringify({ docs: docs, tap: taps }))
    }
    function choose(m) {
        if (!docId || m === mode) return
        var d = {}
        for (var k in docs) d[k] = docs[k]
        if (m === "content") delete d[docId]
        else d[docId] = m
        docs = d
        save()
    }

    function toggleTap() {
        if (!docId) return
        var d = {}
        for (var k in taps) if (taps[k] === true) d[k] = true
        if (tapOn) delete d[docId]
        else d[docId] = true
        taps = d
        save()
    }

    // ─── 刷新标记 ─────────────────────────────────────────────
    function apply() {
        if (mode === "content") {
            if (modeItem) {
                modeItem.mode = modeItem.mContent
                dropping = modeItem
                modeItem = null
            }
            return
        }
        if (!modeItem) {
            try {
                // 枚举值顺手带出来, 本文件不 import xofm.libs.epaper
                modeItem = Qt.createQmlObject(
                    "import QtQuick; import xofm.libs.epaper; ScreenModeItem { anchors.fill: parent;"
                    + " readonly property int mContent: ScreenModeItem.Content;"
                    + " readonly property int mUI: ScreenModeItem.UI; readonly property int mPen: ScreenModeItem.Pen }",
                    root, "fastRefreshMode")
            } catch (e) { log("ScreenModeItem 不可用: " + e); return }
        }
        modeItem.mode = mode === "pen" ? modeItem.mPen : modeItem.mUI
    }
    onModeChanged: apply()

    // ─── 菜单行 ───────────────────────────────────────────────
    function findByName(o, name, depth) {
        if (!o || depth > 60) return null
        try { if (o.visible === false) return null } catch (e) { return null }
        if (o.objectName === name) return o
        var ch = o.children
        if (!ch) return null
        for (var i = 0; i < ch.length; i++) { var r = findByName(ch[i], name, depth + 1); if (r) return r }
        return null
    }
    function attachRow() {
        if (!docView) return
        if (!menuLoader) {
            if (hookTick % 3) return       // 工具栏收起时找不到, 别每秒扫一遍整棵界面树
            var top = docView
            while (top && top.parent) top = top.parent
            menuLoader = findByName(top, "secondaryToolLoader_settingsMenu", 0)
            if (!menuLoader) return
        }
        var sl = findByName(menuLoader, "toggleToolLoader_enableShapesButton", 0)
        if (!sl) return                    // 菜单没打开 (收起时整棵子树不可见)
        if (row && tapRow && row.parent === sl.parent) return
        detachRow()
        // 「启用形状」按钮的高度是只读的, 撑不高; 改成在列表里另起一项: 先追加到末尾,
        // 再把「启用形状」之后的系统项按原顺序挪到它后面 (只换位置, 系统项本身不动)。
        // Repeater 不挪: 换父级会让它重建自己的委托。
        var col = sl.parent, kids = col.children, later = [], seen = false
        for (var i = 0; i < kids.length; i++) {
            if (kids[i] === sl) { seen = true; continue }
            if (seen && ("" + kids[i]).indexOf("QQuickRepeater") !== 0) later.push(kids[i])
        }
        var zh = /[\u4e00-\u9fff]/.test(shapesLabel(sl))
        row = rowComp.createObject(col, { zh: zh })
        tapRow = tapRowComp.createObject(col, { zh: zh })
        for (var k = 0; k < later.length; k++) { later[k].parent = shelf; later[k].parent = col }
    }
    Item { id: shelf; visible: false }  // 挪位置时的临时落脚处
    function shapesLabel(o) {
        try { if (o.text !== undefined && /形状|[Ss]hape/.test("" + o.text)) return "" + o.text } catch (e) {}
        var ch = o.children || []
        for (var i = 0; i < ch.length; i++) { var t = shapesLabel(ch[i]); if (t) return t }
        return ""
    }
    function detachRow() {
        if (row) { row.destroy(); row = null }
        if (tapRow) { tapRow.destroy(); tapRow = null }
    }

    // 一行: 左边「刷新」, 右边三段图标 (高清 图片 / 快刷 风 / 流畅 闪电, 选中段黑底反白, 不配字), 字体字号同系统菜单项
    Component {
        id: rowComp
        Item {
            id: r
            property bool zh: true
            Layout.fillWidth: true
            Layout.preferredHeight: 112
            implicitWidth: 400; implicitHeight: 112
            Text {
                x: 32; y: 36; height: 40
                verticalAlignment: Text.AlignVCenter
                text: r.zh ? "刷新" : "Refresh"
                color: "#000000"
                font.family: "reMarkable Sans"; font.pixelSize: 25; font.weight: Font.Medium
            }
            Rectangle {   // 分段控件: 一个圆角长条三段, 选中的那段填黑、图标反白 (同系统开关一类形状)
                id: seg
                anchors.right: parent.right; anchors.rightMargin: 32
                anchors.verticalCenter: parent.verticalCenter
                width: 3 * 76; height: 56
                radius: height / 2
                color: "#ffffff"
                border.color: "#000000"; border.width: 2
                Row {
                    anchors.fill: parent
                    Repeater {
                        model: ["content", "ui", "pen"]   // 高清 (图片) / 快刷 (风) / 流畅 (闪电)
                        Item {
                            required property string modelData
                            readonly property bool sel: root.mode === modelData
                            width: seg.width / 3; height: seg.height
                            Rectangle {
                                anchors.fill: parent; anchors.margins: 5
                                radius: height / 2
                                color: parent.sel ? "#000000" : "transparent"
                            }
                            Image {
                                anchors.centerIn: parent
                                width: 32; height: 32
                                sourceSize.width: 32; sourceSize.height: 32
                                // 改了图标就把 v 加一 (图片按地址缓存)
                                source: "refresh-icons/" + parent.modelData + (parent.sel ? "-on" : "") + ".svg?v=6"
                            }
                            MouseArea {
                                anchors.fill: parent
                                onClicked: (mouse) => root.choose(parent.modelData)
                            }
                        }
                    }
                }
            }
            MouseArea { anchors.fill: parent; z: -1 }   // 行内空白处吃掉点击, 别穿到下面的文档
        }
    }

    // 「点击翻页」行: 系统开关, 样子同「启用形状」
    Component {
        id: tapRowComp
        Item {
            id: t
            property bool zh: true
            Layout.fillWidth: true
            Layout.preferredHeight: 112
            implicitWidth: 400; implicitHeight: 112
            Text {
                x: 32; y: 36; height: 40
                verticalAlignment: Text.AlignVCenter
                text: t.zh ? "点击翻页" : "Tap to turn pages"
                color: "#000000"
                font.family: "reMarkable Sans"; font.pixelSize: 25; font.weight: Font.Medium
            }
            // 系统开关 (ark.controls Toggle, 样子同「启用形状」); 没有这个模块就用下面自己画的
            property var nativeToggle: null
            Component.onCompleted: {
                try {
                    nativeToggle = Qt.createQmlObject("import QtQuick; import ark.controls as ArkControls; ArkControls.Toggle { x: 280; y: 16 }", t, "tapToggle")
                    nativeToggle.checked = Qt.binding(function () { return root.tapOn })
                } catch (e) { nativeToggle = null }
            }
            Rectangle {
                visible: !t.nativeToggle
                x: 280; y: 32; width: 88; height: 48
                radius: height / 2
                color: root.tapOn ? "#000000" : "#ffffff"
                border.color: "#000000"; border.width: 2
                Rectangle {
                    width: 36; height: 36; radius: 18
                    anchors.verticalCenter: parent.verticalCenter
                    x: root.tapOn ? parent.width - width - 6 : 6
                    color: root.tapOn ? "#ffffff" : "#000000"
                }
            }
            MouseArea {                // 整行都能点; 盖在开关上面, 开关只跟着状态显示
                anchors.fill: parent
                z: 10
                onClicked: (mouse) => root.toggleTap()
            }
        }
    }

    // ─── 点击翻页 ─────────────────────────────────────────────
    readonly property bool penClose: docView ? docView.penClose === true : false
    property double penLeftAt: 0
    onPenCloseChanged: if (!penClose) penLeftAt = Date.now()
    function menuOpen(o, depth) {
        if (!o || depth > 40) return false
        try { if (o.visible === false) return false } catch (e) { return false }
        if (("" + o).indexOf("ToolbarFoldout") === 0 && o.height > 0) return true
        var ch = o.children || []
        for (var i = 0; i < ch.length; i++) if (menuOpen(ch[i], depth + 1)) return true
        return false
    }
    function tapTurn(p) {
        if (!tapOn || !docView) return
        var dir = p.x < width / 3 ? -1 : (p.x > width * 2 / 3 ? 1 : 0)
        if (dir === 0 || p.y < 100) return                          // 中间 / 顶部工具栏那条留给系统
        if (penClose || Date.now() - penLeftAt < 1000) return       // 拿着笔
        if (docView.subDialogOpen || menuOpen(docView, 0)) return   // 弹窗 / 菜单开着
        var n = docView.currentPage + dir
        if (n >= 0 && n < docView.pageCount) docView.openPage(n)
    }
    TapHandler {
        enabled: root.tapOn
        acceptedDevices: PointerDevice.TouchScreen
        gesturePolicy: TapHandler.DragThreshold
        onTapped: (eventPoint, button) => root.tapTurn(eventPoint.position)
    }

    onHookTickChanged: {
        if (dropping) { dropping.destroy(); dropping = null }
        if (!loaded) return
        attachRow()
    }
    Component.onCompleted: load()
    Component.onDestruction: {
        if (modeItem) modeItem.mode = modeItem.mContent
        detachRow()
    }
}
