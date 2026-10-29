// 输入法 — 「空间」内置设置项: 拼音 / 五笔方案切换 (直连 ime-server)
import QtQuick
import QtQuick.Layouts
import device.ui.controls

Item {
    id: root
    property var space          // 外壳注入的 API 对象 (见 space/README.md)
    anchors.fill: parent

    property var _rmhImeSchemas: []
    property string _rmhImeSchema: ""
    property string _rmhImeStatus: ""

    Component.onCompleted: imeLoadSchema()

    ColumnLayout {
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.top: parent.top
        spacing: 24

        RowLayout {
            Layout.fillWidth: true
            Text {
                text: space.tr("title")
                font.pixelSize: 32
                font.weight: Font.Medium
            }
            Item { Layout.fillWidth: true }
            IconButton {
                iconSource: "qrc:/ark/icons/restore"
                title: space.tr("refresh")
                onClicked: root.imeLoadSchema()
            }
        }

        Text {
            visible: root._rmhImeStatus !== ""
            text: root._rmhImeStatus
            font.pixelSize: 24
            Layout.fillWidth: true
        }

        Repeater {
            model: root._rmhImeSchemas
            delegate: Item {
                id: _rmhImeRow
                required property var modelData
                Layout.fillWidth: true
                Layout.preferredHeight: 96
                readonly property bool rowApplied:
                    root._rmhImeSchema === modelData.id

                Rectangle {
                    anchors.fill: parent
                    radius: 6
                    color: _rmhImeRow.rowApplied ? "#eeeeee" : "transparent"
                    border.color: _rmhImeRow.rowApplied ? "#333333" : "#e0e0e0"
                    border.width: 1
                }
                RowLayout {
                    anchors.left: parent.left
                    anchors.right: parent.right
                    anchors.verticalCenter: parent.verticalCenter
                    anchors.leftMargin: 24
                    anchors.rightMargin: 24
                    spacing: 16
                    Text {
                        Layout.fillWidth: true
                        text: _rmhImeRow.modelData.name + "  (" + _rmhImeRow.modelData.id + ")"
                        font.pixelSize: 26
                        font.weight: _rmhImeRow.rowApplied ? Font.Medium : Font.Normal
                        elide: Text.ElideMiddle
                    }
                    Text {
                        visible: _rmhImeRow.rowApplied
                        text: space.tr("active")
                        font.pixelSize: 22
                    }
                }
                MouseArea {
                    anchors.fill: parent
                    onClicked: root.imeSelectSchema(_rmhImeRow.modelData.id)
                }
            }
        }

        Text {
            Layout.fillWidth: true
            wrapMode: Text.WordWrap
            font.pixelSize: 22
            color: "#000000"
            text: space.tr("hint")
        }
    }

    function imeLoadSchema() {
        var x = new XMLHttpRequest()
        x.onreadystatechange = function() {
            if (x.readyState !== XMLHttpRequest.DONE) return
            if (x.status !== 200) {
                root._rmhImeStatus = space.tr("svc_down")
                return
            }
            try {
                var r = JSON.parse(x.responseText)
                root._rmhImeSchemas = r.available || []
                root._rmhImeSchema = r.schema || ""
                root._rmhImeStatus = ""
            } catch (e) {
                root._rmhImeStatus = space.tr("parse_failed")
            }
        }
        x.open("GET", "http://127.0.0.1:19876/rime/schema")
        x.send()
    }
    function imeSelectSchema(id) {
        if (!id || id === root._rmhImeSchema) return
        root._rmhImeStatus = space.tr("switching")
        var x = new XMLHttpRequest()
        x.onreadystatechange = function() {
            if (x.readyState !== XMLHttpRequest.DONE) return
            var ok = false
            try { ok = x.status === 200 && JSON.parse(x.responseText).ok === true } catch (e) {}
            root._rmhImeStatus = ok ? "" : space.tr("switch_failed")
            root.imeLoadSchema()
        }
        x.open("GET", "http://127.0.0.1:19876/rime/schema?id=" + encodeURIComponent(id))
        x.send()
    }
    }
