// 个性化 — 「空间」内置设置项: 主题 / 字体 / 休眠壁纸 / 空间插图
//
// 三样都是"换个样子"的事, 原来字体与壁纸上下堆在一张长页里, 插图又单独藏在空间设置
// 的二级页 —— 同一类事分在两处。改成三个页签并到这里。
import QtQuick
import QtQuick.Layouts
import device.ui.controls

Item {
    id: root
    property var space          // 外壳注入的 API 对象 (见 space/README.md)
    anchors.fill: parent

    // 已生效 (从后端拉到) vs 选中 (用户在页内的临时选择)
    property string _rmhAppliedFont: ""
    property string _rmhAppliedScreen: ""
    property string _rmhSelectedFont: ""
    property string _rmhSelectedScreen: ""
    readonly property bool _rmhDirty:
        _rmhSelectedFont !== _rmhAppliedFont ||
        _rmhSelectedScreen !== _rmhAppliedScreen
    ListModel { id: _rmhFontsModel }
    ListModel { id: _rmhScreensModel }

    // ─── 空间插图 ───
    // 首页顶部那张大卡垫什么。选择存在外壳自己的配置里 (应用 id "shell"), 所以这里
    // 读写的是别人的数据 —— 外壳在关掉应用时会重新读一遍, 改完退出就生效。
    //   ""      跟着装了什么走: 有天气就天气, 没有就内置那幅
    //   "none"  就要内置那幅, 哪怕装着天气
    //   其它    用户自己传的文件名
    property string _rmhHero: ""
    ListModel { id: _rmhHeroModel }
    property string sect: "theme"         // theme / font / screen / hero

    // ─── 主题 (internal/space/theme.go) ───
    // 点一个就换 (存在后台, 外壳马上重读); 商店里的主题装好后自动出现在这里。
    property string _rmhTheme: ""
    ListModel { id: _rmhThemeModel }
    function refreshThemes() {
        space.get("/space/themes", function (st, r) {
            if (st !== 200 || !r) return
            _rmhThemeModel.clear()
            root._rmhTheme = r.active || "default"
            for (var i = 0; i < (r.themes || []).length; ++i) {
                var t = r.themes[i]
                _rmhThemeModel.append({ tid: t.id, tname: (space.lang !== "zh-CN" && t.name_en) ? t.name_en : t.name, tpreview: t.preview || "" })
            }
        })
    }
    function setTheme(id) {
        root._rmhTheme = id
        space.request("PUT", space.baseUrl + "/space/theme", { id: id }, function (st) {
            if (st === 200) space.reloadTheme()
            else root.refreshThemes()
        })
    }

    function refreshHero() {
        _rmhHeroModel.clear()
        space.get("/hero", function (st, r) {
            if (st !== 200 || !r) return
            for (var i = 0; i < r.length; ++i) _rmhHeroModel.append({ rowName: r[i].name })
        })
        space.dataGet("shell", "config", function (st, r) {
            if (st === 200 && r && r.hero !== undefined) root._rmhHero = r.hero
        })
    }
    function setHero(key) {
        root._rmhHero = key
        // 只改 hero 这一项, 别把外壳的其它设置 (刷新效果) 覆盖掉
        space.dataGet("shell", "config", function (st, r) {
            var cfg = (st === 200 && r) ? r : ({})
            cfg.hero = key
            space.dataPut("shell", "config", cfg, null)
        })
    }
    function deleteHero(name) {
        space.request("DELETE", space.baseUrl + "/hero/" + encodeURIComponent(name),
                      null, function (st) {
            if (st === 200) {
                if (root._rmhHero === name) root.setHero("")
                root.refreshHero()
            }
        })
    }

    Component.onCompleted: { refreshThemes(); refreshFonts(); refreshScreens(); refreshHero() }

    // 有变更时右上角出space.tr("apply") (放在外壳标题那一行的右侧)
    IconButton {
        id: _rmhApply
        visible: root._rmhDirty
        z: 2
        anchors.bottom: parent.top
        anchors.bottomMargin: Math.round(48 * root.space.unit)
        anchors.right: parent.right
        iconSource: "qrc:/ark/icons/checkmark"
        title: space.tr("apply")
        onClicked: _rmhConfirmApply.visible = true
    }

    Flickable {
        anchors.fill: parent
        contentWidth: width
        contentHeight: _rmhPersCol.implicitHeight
        clip: true
        boundsBehavior: Flickable.StopAtBounds

        ColumnLayout {
            id: _rmhPersCol
            width: parent.width
            spacing: 24

            // ─── 页签 ────────────────────────────────────
            Row {
                Layout.fillWidth: true
                Layout.bottomMargin: 8
                spacing: 0
                Repeater {
                    model: [{ k: "theme", t: space.tr("tab_theme") }, { k: "font", t: space.tr("tab_font") },
                            { k: "screen", t: space.tr("tab_wallpaper") }, { k: "hero", t: space.tr("tab_hero") }]
                    delegate: Rectangle {
                        required property var modelData
                        readonly property bool on: root.sect === modelData.k
                        width: Math.round((_rmhPersCol.width - 2) / 4)
                        height: 68
                        color: on ? "#262626" : "white"
                        border.color: "#B4AFA6"
                        border.width: 1
                        Text {
                            anchors.centerIn: parent
                            text: parent.modelData.t
                            font.pixelSize: 26
                            font.weight: parent.on ? Font.Medium : Font.Normal
                            color: parent.on ? "white" : "#262626"
                        }
                        MouseArea { anchors.fill: parent; onClicked: root.sect = parent.modelData.k }
                    }
                }
            }

            // ─── 主题 ────────────────────────────────────
            RowLayout {
                visible: root.sect === "theme"
                Layout.fillWidth: true
                Text { text: space.tr("tab_theme"); font.pixelSize: 32; font.weight: Font.Medium }
                Item { Layout.fillWidth: true }
                IconButton {
                    iconSource: "qrc:/ark/icons/restore"
                    title: space.tr("refresh")
                    onClicked: root.refreshThemes()
                }
            }
            Text {
                visible: root.sect === "theme"
                Layout.fillWidth: true
                text: space.tr("theme_hint")
                font.pixelSize: 22
                color: "#4A4A46"
                wrapMode: Text.WordWrap
            }
            Repeater {
                model: _rmhThemeModel
                delegate: Rectangle {
                    id: _rmhThemeCell
                    required property string tid
                    required property string tname
                    required property string tpreview
                    visible: root.sect === "theme"
                    readonly property bool sel: root._rmhTheme === tid
                    Layout.fillWidth: true
                    Layout.preferredHeight: tpreview !== "" ? 72 + Math.round(width / 2) : 88
                    radius: 8
                    color: "white"
                    border.color: sel ? "#262626" : "#C4BFB6"
                    border.width: sel ? 3 : 1
                    clip: true
                    Text {
                        anchors.top: parent.top; anchors.topMargin: 24
                        anchors.left: parent.left; anchors.leftMargin: 20
                        text: _rmhThemeCell.tname
                        font.pixelSize: 26
                        font.weight: _rmhThemeCell.sel ? Font.Medium : Font.Normal
                    }
                    Text {
                        visible: _rmhThemeCell.sel
                        anchors.top: parent.top; anchors.topMargin: 28
                        anchors.right: parent.right; anchors.rightMargin: 20
                        text: space.tr("active")
                        font.pixelSize: 22
                        color: "#262626"
                    }
                    Image {
                        anchors.left: parent.left; anchors.right: parent.right
                        anchors.bottom: parent.bottom
                        anchors.leftMargin: 1; anchors.rightMargin: 1; anchors.bottomMargin: 1
                        height: Math.round(width / 2)
                        source: _rmhThemeCell.tpreview
                        sourceSize.width: width
                        fillMode: Image.PreserveAspectCrop
                        visible: _rmhThemeCell.tpreview !== "" && status === Image.Ready
                    }
                    MouseArea { anchors.fill: parent; onClicked: root.setTheme(_rmhThemeCell.tid) }
                }
            }
            Text {
                visible: root.sect === "theme"
                Layout.fillWidth: true
                Layout.topMargin: 8
                text: space.tr("theme_more")
                font.pixelSize: 24
                font.underline: true
                MouseArea { anchors.fill: parent; anchors.margins: -12; onClicked: space.openStore() }
            }

            // ─── 字体 ────────────────────────────────────
            RowLayout {
                visible: root.sect === "font"
                Layout.fillWidth: true
                Text {
                    text: space.tr("tab_font")
                    font.pixelSize: 32
                    font.weight: Font.Medium
                }
                Item { Layout.fillWidth: true }
                IconButton {
                    iconSource: "qrc:/ark/icons/restore"
                    title: space.tr("refresh")
                    onClicked: root.refreshFonts()
                }
            }

            Text {
                visible: root.sect === "font" && _rmhFontsModel.count === 0
                text: space.tr("font_empty")
                font.pixelSize: 24
                Layout.fillWidth: true
            }

            Repeater {
                model: _rmhFontsModel
                delegate: Item {
                    visible: root.sect === "font"
                    id: _rmhFontRow
                    required property string rowName
                    Layout.fillWidth: true
                    Layout.preferredHeight: 96

                    readonly property bool rowSelected:
                        root._rmhSelectedFont === rowName
                    readonly property bool rowApplied:
                        root._rmhAppliedFont === rowName

                    Rectangle {
                        anchors.fill: parent
                        radius: 6
                        color: _rmhFontRow.rowSelected ? "#eeeeee" : "transparent"
                        border.color: _rmhFontRow.rowSelected ? "#333333" : "#e0e0e0"
                        border.width: 1
                    }

                    RowLayout {
                        anchors.left: parent.left
                        anchors.right: parent.right
                        anchors.verticalCenter: parent.verticalCenter
                        anchors.leftMargin: 24
                        anchors.rightMargin: 12
                        spacing: 16

                        Text {
                            Layout.fillWidth: true
                            Layout.alignment: Qt.AlignVCenter
                            text: _rmhFontRow.rowName
                            font.pixelSize: 26
                            font.weight: _rmhFontRow.rowSelected ? Font.Medium : Font.Normal
                            elide: Text.ElideMiddle
                            verticalAlignment: Text.AlignVCenter
                        }
                        Text {
                            Layout.alignment: Qt.AlignVCenter
                            visible: _rmhFontRow.rowApplied
                            text: space.tr("active")
                            font.pixelSize: 22
                            verticalAlignment: Text.AlignVCenter
                        }
                        IconButton {
                            Layout.alignment: Qt.AlignVCenter
                            iconSource: "qrc:/ark/icons/trashcan"
                            title: ""
                            onClicked: root.deleteFont(_rmhFontRow.rowName)
                        }
                    }

                    MouseArea {
                        anchors.fill: parent
                        // 给 trashcan 按钮区域留点击空间 (右侧 96px)
                        anchors.rightMargin: 96
                        onClicked: root._rmhSelectedFont = _rmhFontRow.rowName
                    }
                }
            }

            // ─── 壁纸 ────────────────────────────────────
            RowLayout {
                visible: root.sect === "screen"
                Layout.fillWidth: true
                Text {
                    text: space.tr("tab_wallpaper")
                    font.pixelSize: 32
                    font.weight: Font.Medium
                }
                Item { Layout.fillWidth: true }
                IconButton {
                    iconSource: "qrc:/ark/icons/restore"
                    title: space.tr("refresh")
                    onClicked: root.refreshScreens()
                }
            }

            Text {
                visible: root.sect === "screen" && _rmhScreensModel.count === 0
                text: space.tr("wallpaper_empty")
                font.pixelSize: 24
                Layout.fillWidth: true
            }

            Flow {
                visible: root.sect === "screen"
                Layout.fillWidth: true
                spacing: 20

                Repeater {
                    model: _rmhScreensModel
                    delegate: Rectangle {
                        id: _rmhScreenCell
                        required property string rowName
                        width: 240
                        height: 380
                        radius: 8
                        clip: true

                        readonly property bool rowSelected:
                            root._rmhSelectedScreen === rowName
                        readonly property bool rowApplied:
                            root._rmhAppliedScreen === rowName

                        color: _rmhScreenCell.rowSelected ? "#eeeeee" : "transparent"
                        border.color: _rmhScreenCell.rowSelected ? "#333333" : "#cccccc"
                        border.width: _rmhScreenCell.rowSelected ? 3 : 1

                        ColumnLayout {
                            anchors.fill: parent
                            anchors.margins: 12
                            spacing: 8

                            Image {
                                Layout.fillWidth: true
                                Layout.preferredHeight: 200
                                source: root.space.baseUrl
                                    + "/screens/" + _rmhScreenCell.rowName + "/preview"
                                cache: false
                                fillMode: Image.PreserveAspectFit
                            }

                            Text {
                                Layout.fillWidth: true
                                horizontalAlignment: Text.AlignHCenter
                                text: _rmhScreenCell.rowName
                                font.pixelSize: 18
                                font.weight: _rmhScreenCell.rowSelected ? Font.Medium : Font.Normal
                                elide: Text.ElideMiddle
                                wrapMode: Text.NoWrap
                            }

                            Text {
                                visible: _rmhScreenCell.rowApplied
                                Layout.fillWidth: true
                                horizontalAlignment: Text.AlignHCenter
                                text: space.tr("active")
                                font.pixelSize: 16
                                color: "#666666"
                            }

                            Item { Layout.fillHeight: true }

                            RowLayout {
                                Layout.fillWidth: true
                                spacing: 0
                                Item { Layout.fillWidth: true }
                                IconButton {
                                    iconSource: "qrc:/ark/icons/trashcan"
                                    title: ""
                                    onClicked: root.deleteScreen(_rmhScreenCell.rowName)
                                }
                            }
                        }

                        // 卡片整体点击 = 选中 (避开底部删除按钮区域)
                        MouseArea {
                            anchors.fill: parent
                            anchors.bottomMargin: 72
                            onClicked: root._rmhSelectedScreen = _rmhScreenCell.rowName
                        }
                    }
                }
            }

            // ─── 空间插图 ────────────────────────────────
            // 选项做成带预览的卡片 —— 光给文件名看不出是哪张画, 和挑壁纸一个道理。
            // 前两项是特殊选择 (跟随应用 / 内置那幅), 后面才是用户自己传的。
            RowLayout {
                visible: root.sect === "hero"
                Layout.fillWidth: true
                Text { text: space.tr("hero_title"); font.pixelSize: 32; font.weight: Font.Medium }
                Item { Layout.fillWidth: true }
                IconButton {
                    iconSource: "qrc:/ark/icons/restore"
                    title: space.tr("refresh")
                    onClicked: root.refreshHero()
                }
            }
            Text {
                visible: root.sect === "hero"
                Layout.fillWidth: true
                text: space.tr("hero_hint_1")
                      + space.tr("hero_hint_2")
                font.pixelSize: 22
                color: "#4A4A46"
                wrapMode: Text.WordWrap
            }
            Repeater {
                model: 2 + _rmhHeroModel.count
                delegate: Rectangle {
                    id: _rmhHeroCell
                    required property int index
                    visible: root.sect === "hero"
                    readonly property string rowKey:
                        index === 0 ? "" : (index === 1 ? "none"
                                            : _rmhHeroModel.get(index - 2).rowName)
                    readonly property string rowLabel:
                        index === 0 ? space.tr("hero_auto") : (index === 1 ? space.tr("hero_builtin") : rowKey)
                    readonly property string rowPreview:
                        index === 0 ? ""
                        : (index === 1 ? ((root.space.theme && root.space.theme.hero) || root.space.kitDir + "/images/hero-default.jpg")
                           : root.space.baseUrl + "/hero/" + encodeURIComponent(rowKey))
                    readonly property bool sel: root._rmhHero === rowKey

                    Layout.fillWidth: true
                    Layout.preferredHeight: 72 + Math.round(width / 2)
                    radius: 8
                    color: "white"
                    border.color: sel ? "#262626" : "#C4BFB6"
                    border.width: sel ? 3 : 1
                    clip: true

                    Text {
                        anchors.top: parent.top; anchors.topMargin: 18
                        anchors.left: parent.left; anchors.leftMargin: 20
                        text: _rmhHeroCell.rowLabel
                        font.pixelSize: 26
                        font.weight: _rmhHeroCell.sel ? Font.Medium : Font.Normal
                    }
                    // 自己传的才给删除
                    IconButton {
                        visible: _rmhHeroCell.index >= 2
                        anchors.top: parent.top; anchors.topMargin: 6
                        anchors.right: parent.right; anchors.rightMargin: 10
                        iconSource: "qrc:/ark/icons/trash"
                        title: space.tr("delete")
                        onClicked: root.deleteHero(_rmhHeroCell.rowKey)
                    }
                    Image {
                        anchors.left: parent.left; anchors.right: parent.right
                        anchors.bottom: parent.bottom
                        anchors.leftMargin: 1; anchors.rightMargin: 1; anchors.bottomMargin: 1
                        height: Math.round(width / 2)
                        source: _rmhHeroCell.rowPreview
                        sourceSize.width: width
                        cache: false
                        fillMode: Image.PreserveAspectCrop
                        visible: _rmhHeroCell.rowPreview !== "" && status === Image.Ready
                    }
                    Text {
                        anchors.centerIn: parent
                        anchors.verticalCenterOffset: 18
                        width: parent.width - 80
                        visible: _rmhHeroCell.rowPreview === ""
                        text: space.tr("hero_auto_hint")
                        font.pixelSize: 22
                        color: "#6B6B66"
                        wrapMode: Text.WordWrap
                        horizontalAlignment: Text.AlignHCenter
                    }
                    MouseArea {
                        anchors.fill: parent
                        anchors.topMargin: 48      // 让开右上角的删除按钮
                        onClicked: root.setHero(_rmhHeroCell.rowKey)
                    }
                }
            }

        }

    }

    // ─── 应用确认对话框 ─────────────────────────────────────────
    Rectangle {
        id: _rmhConfirmApply
        anchors.fill: parent
        color: "#80000000"
        visible: false
        z: 1
        MouseArea { anchors.fill: parent }

        Rectangle {
            anchors.centerIn: parent
            width: 540
            height: 280
            radius: 8
            color: "white"
            border.color: "#cccccc"

            ColumnLayout {
                anchors.fill: parent
                anchors.margins: 24
                spacing: 12

                Text {
                    text: space.tr("confirm_title")
                    font.pixelSize: 26
                    font.weight: Font.Medium
                }
                Text {
                    visible: root._rmhSelectedFont !== root._rmhAppliedFont
                    text: space.tr("confirm_font") + root._rmhSelectedFont
                    font.pixelSize: 20
                    color: "#444444"
                    wrapMode: Text.WordWrap
                    elide: Text.ElideMiddle
                    Layout.fillWidth: true
                }
                Text {
                    visible: root._rmhSelectedScreen !== root._rmhAppliedScreen
                    text: space.tr("confirm_wallpaper") + root._rmhSelectedScreen
                    font.pixelSize: 20
                    color: "#444444"
                    wrapMode: Text.WordWrap
                    elide: Text.ElideMiddle
                    Layout.fillWidth: true
                }
                Text {
                    text: space.tr("confirm_restart")
                    font.pixelSize: 22
                    Layout.fillWidth: true
                }
                Item { Layout.fillHeight: true }
                RowLayout {
                    Layout.alignment: Qt.AlignRight
                    spacing: 12
                    IconButton {
                        iconSource: "qrc:/ark/icons/chevron_left"
                        title: space.tr("cancel")
                        onClicked: _rmhConfirmApply.visible = false
                    }
                    IconButton {
                        iconSource: "qrc:/ark/icons/checkmark"
                        title: space.tr("confirm")
                        onClicked: {
                            _rmhConfirmApply.visible = false
                            root.applyAll()
                        }
                    }
                }
            }
        }
    }

    function refreshFonts() {
        var x = new XMLHttpRequest()
        x.onreadystatechange = function() {
            if (x.readyState !== 4) return
            _rmhFontsModel.clear()
            if (x.status !== 200) return
            try {
                var arr = JSON.parse(x.responseText)
                var active = ""
                for (var i = 0; i < arr.length; i++) {
                    _rmhFontsModel.append({ rowName: arr[i].name })
                    if (arr[i].active) active = arr[i].name
                }
                root._rmhAppliedFont = active
                root._rmhSelectedFont = active
            } catch (e) {}
        }
        x.open("GET", root.space.baseUrl + "/fonts")
        x.send()
    }
    function refreshScreens() {
        var x = new XMLHttpRequest()
        x.onreadystatechange = function() {
            if (x.readyState !== 4) return
            _rmhScreensModel.clear()
            if (x.status !== 200) return
            try {
                var arr = JSON.parse(x.responseText)
                var active = ""
                for (var i = 0; i < arr.length; i++) {
                    _rmhScreensModel.append({ rowName: arr[i].name })
                    if (arr[i].active) active = arr[i].name
                }
                root._rmhAppliedScreen = active
                root._rmhSelectedScreen = active
            } catch (e) {}
        }
        x.open("GET", root.space.baseUrl + "/screens")
        x.send()
    }
    function deleteFont(name) {
        var x = new XMLHttpRequest()
        x.onreadystatechange = function() {
            if (x.readyState !== 4) return
            refreshFonts()
        }
        x.open("DELETE", root.space.baseUrl + "/fonts/" + encodeURIComponent(name))
        x.send()
    }
    function deleteScreen(name) {
        var x = new XMLHttpRequest()
        x.onreadystatechange = function() {
            if (x.readyState !== 4) return
            refreshScreens()
        }
        x.open("DELETE", root.space.baseUrl + "/screens/" + encodeURIComponent(name))
        x.send()
    }
    function applyAll() {
        var body = {}
        if (root._rmhSelectedFont !== root._rmhAppliedFont) {
            body.font = root._rmhSelectedFont
        }
        if (root._rmhSelectedScreen !== root._rmhAppliedScreen) {
            body.screen = root._rmhSelectedScreen
        }
        if (!body.font && !body.screen) return
        var x = new XMLHttpRequest()
        x.open("POST", root.space.baseUrl + "/apply")
        x.setRequestHeader("Content-Type", "application/json")
        x.send(JSON.stringify(body))
        // xochitl 即将重启, 先把启动台关掉避免视觉残留
        root.space.closeSpace()
    }
    }
