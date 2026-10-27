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
// 菜单行作为列表 (ColumnLayout) 里独立的一项, 插在「启用形状」后面 (做法见 attachRow)。
// 运行时注入的组件里 Timer 不触发, 所有"等一会"都靠注入库每秒递增的 hookTick。
import QtQuick
import QtQuick.Layouts
import xofm.libs.epaper

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

    property var modeItem: null         // 整屏的刷新标记
    property var dropping: null         // 已改回 Content、等下一拍销毁的旧标记
    property var menuLoader: null       // 系统菜单的「…」按钮 (secondaryToolLoader_settingsMenu), 找到就缓存
    property var row: null              // 我们插进菜单的那一行

    function log(s) { console.warn("[fastrefresh] " + s) }

    function load() {
        var x = new XMLHttpRequest()
        x.onreadystatechange = function () {
            if (x.readyState !== XMLHttpRequest.DONE) return
            var d = {}
            if (x.status === 200) { try { d = (JSON.parse(x.responseText) || {}).docs || {} } catch (e) {} }
            root.docs = d
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
        x.send(JSON.stringify({ docs: docs }))
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

    // ─── 刷新标记 ─────────────────────────────────────────────
    function apply() {
        if (mode === "content") {
            if (modeItem) {
                modeItem.mode = ScreenModeItem.Content
                dropping = modeItem
                modeItem = null
            }
            return
        }
        if (!modeItem) {
            try {
                modeItem = Qt.createQmlObject(
                    "import QtQuick; import xofm.libs.epaper; ScreenModeItem { anchors.fill: parent }",
                    root, "fastRefreshMode")
            } catch (e) { log("ScreenModeItem 不可用: " + e); return }
        }
        modeItem.mode = mode === "pen" ? ScreenModeItem.Pen : ScreenModeItem.UI
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
        if (row && row.parent === sl.parent) return
        detachRow()
        // 「启用形状」按钮的高度是只读的, 撑不高; 改成在列表里另起一项: 先追加到末尾,
        // 再把「启用形状」之后的系统项按原顺序挪到它后面 (只换位置, 系统项本身不动)。
        // Repeater 不挪: 换父级会让它重建自己的委托。
        var col = sl.parent, kids = col.children, later = [], seen = false
        for (var i = 0; i < kids.length; i++) {
            if (kids[i] === sl) { seen = true; continue }
            if (seen && ("" + kids[i]).indexOf("QQuickRepeater") !== 0) later.push(kids[i])
        }
        row = rowComp.createObject(col, { zh: /[一-鿿]/.test(shapesLabel(sl)) })
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
    }

    // 一行: 左边「刷新」, 右边三段 (选中的黑底白字), 字体字号同系统菜单项
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
            Rectangle {
                id: seg
                anchors.right: parent.right; anchors.rightMargin: 32
                anchors.verticalCenter: parent.verticalCenter
                width: 3 * 76; height: 52
                radius: height / 2
                color: "#ffffff"
                border.color: "#000000"; border.width: 2
                clip: true
                Row {
                    anchors.fill: parent
                    Repeater {
                        model: [["content", "高清", "HD"], ["ui", "快刷", "Fast"], ["pen", "流畅", "Smooth"]]
                        Item {
                            required property var modelData
                            readonly property bool sel: root.mode === modelData[0]
                            width: seg.width / 3; height: seg.height
                            Rectangle {
                                anchors.fill: parent; anchors.margins: 4
                                radius: height / 2
                                color: parent.sel ? "#000000" : "transparent"
                            }
                            Text {
                                anchors.centerIn: parent
                                text: r.zh ? modelData[1] : modelData[2]
                                color: parent.sel ? "#ffffff" : "#000000"
                                font.family: "reMarkable Sans"; font.pixelSize: 22; font.weight: Font.Medium
                            }
                            MouseArea {
                                anchors.fill: parent
                                onClicked: (mouse) => root.choose(parent.modelData[0])
                            }
                        }
                    }
                }
            }
            MouseArea { anchors.fill: parent; z: -1 }   // 行内空白处吃掉点击, 别穿到下面的文档
        }
    }

    onHookTickChanged: {
        if (dropping) { dropping.destroy(); dropping = null }
        if (!loaded) return
        attachRow()
    }
    Component.onCompleted: load()
    Component.onDestruction: {
        if (modeItem) modeItem.mode = ScreenModeItem.Content
        detachRow()
    }
}
