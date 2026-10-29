// 蓝牙 — 「空间」内置应用: 耳机扫描 / 配对 / 连接 (后端 upload-server /bt/*)
import QtQuick
import QtQuick.Layouts
import device.ui.controls

Item {
    id: root
    property var space          // 外壳注入的 API 对象 (见 space/README.md)
    anchors.fill: parent

    property var _rmhBtDevices: []
    property string _rmhBtStatus: ""
    property bool _rmhBtBusy: false

    Component.onCompleted: btLoad()

    function btRequest(method, path, body, cb) {
        var x = new XMLHttpRequest()
        x.onreadystatechange = function() {
            if (x.readyState !== 4) return
            var r = null
            try { r = JSON.parse(x.responseText) } catch (e) { r = null }
            cb(x.status, r)
        }
        x.open(method, root.space.baseUrl + path)
        x.setRequestHeader("Content-Type", "application/json")
        x.send(body ? JSON.stringify(body) : null)
    }
    function btLoad() {
        btRequest("GET", "/bt/status", null, function(st, r) {
            if (st !== 200 || !r) { root._rmhBtStatus = space.tr("backend_down", { code: st }); return }
            root._rmhBtDevices = r.devices || []
            root._rmhBtStatus = r.adapter === "powered" ? "" : (r.adapter === "absent" ? space.tr("bt_off_hint") : space.tr("bt_off"))
            if (!r.audioReady) root._rmhBtStatus += (root._rmhBtStatus ? "\uff1b" : "") + space.tr("no_audio")
        })
    }
    function btScan() {
        root._rmhBtBusy = true
        root._rmhBtStatus = space.tr("scanning")
        btRequest("POST", "/bt/scan?seconds=10", null, function(st, r) {
            root._rmhBtBusy = false
            if (st !== 200 || !r) { root._rmhBtStatus = space.tr("scan_failed", { err: (r && r.detail ? r.detail : st) }); return }
            root._rmhBtDevices = r.devices || []
            root._rmhBtStatus = (r.devices || []).length ? "" : space.tr("nothing_found")
        })
    }
    function btAction(action, mac) {
        root._rmhBtBusy = true
        root._rmhBtStatus = (action === "pair" ? space.tr("pairing") : action === "connect" ? space.tr("connecting") : space.tr("working"))
        btRequest("POST", "/bt/" + action, { mac: mac }, function(st, r) {
            root._rmhBtBusy = false
            root._rmhBtStatus = (st === 200) ? "" : ((r && r.detail) ? r.detail : space.tr("failed", { code: st }))
            root.btLoad()
        })
    }

    ColumnLayout {
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.top: parent.top
        spacing: 20

        RowLayout {
            Layout.fillWidth: true
            Text { text: space.tr("headset"); font.pixelSize: 32; font.weight: Font.Medium }
            Item { Layout.fillWidth: true }
            Rectangle {
                Layout.preferredWidth: 200; Layout.preferredHeight: 64; radius: 8
                color: "#000000"; opacity: root._rmhBtBusy ? 0.4 : 1
                Text { anchors.centerIn: parent; color: "#ffffff"; font.pixelSize: 24; text: space.tr("scan") }
                MouseArea { anchors.fill: parent; enabled: !root._rmhBtBusy; onClicked: root.btScan() }
            }
        }
        Text {
            visible: root._rmhBtStatus !== ""
            text: root._rmhBtStatus
            font.pixelSize: 22; color: "#000000"; wrapMode: Text.WordWrap; Layout.fillWidth: true
        }
        Text {
            visible: root._rmhBtDevices.length === 0
            text: space.tr("empty")
            font.pixelSize: 24; color: "#000000"; wrapMode: Text.WordWrap; Layout.fillWidth: true
        }
        Repeater {
            model: root._rmhBtDevices
            delegate: Item {
                id: _rmhBtRow
                required property var modelData
                Layout.fillWidth: true
                Layout.preferredHeight: 96
                Rectangle { anchors.fill: parent; radius: 6; color: _rmhBtRow.modelData.connected ? "#eeeeee" : "transparent"; border.color: "#e0e0e0"; border.width: 1 }
                RowLayout {
                    anchors.left: parent.left; anchors.right: parent.right; anchors.verticalCenter: parent.verticalCenter
                    anchors.leftMargin: 24; anchors.rightMargin: 24; spacing: 16
                    ColumnLayout {
                        Layout.fillWidth: true; spacing: 2
                        Text { text: _rmhBtRow.modelData.name; font.pixelSize: 26; elide: Text.ElideRight; Layout.fillWidth: true }
                        Text { text: _rmhBtRow.modelData.mac + (_rmhBtRow.modelData.connected ? space.tr("connected") : (_rmhBtRow.modelData.paired ? space.tr("paired") : "")); font.pixelSize: 20; color: "#000000" }
                    }
                    Rectangle {
                        Layout.preferredWidth: 150; Layout.preferredHeight: 56; radius: 6
                        color: "transparent"; border.color: "#333333"; border.width: 1
                        Text { anchors.centerIn: parent; font.pixelSize: 22; text: _rmhBtRow.modelData.connected ? space.tr("disconnect") : (_rmhBtRow.modelData.paired ? space.tr("connect") : space.tr("pair")) }
                        MouseArea { anchors.fill: parent; enabled: !root._rmhBtBusy; onClicked: root.btAction(_rmhBtRow.modelData.connected ? "disconnect" : (_rmhBtRow.modelData.paired ? "connect" : "pair"), _rmhBtRow.modelData.mac) }
                    }
                    Rectangle {
                        visible: _rmhBtRow.modelData.paired && !_rmhBtRow.modelData.connected
                        Layout.preferredWidth: 110; Layout.preferredHeight: 56; radius: 6
                        color: "transparent"; border.color: "#bbbbbb"; border.width: 1
                        Text { anchors.centerIn: parent; font.pixelSize: 22; color: "#000000"; text: space.tr("forget") }
                        MouseArea { anchors.fill: parent; enabled: !root._rmhBtBusy; onClicked: root.btAction("remove", _rmhBtRow.modelData.mac) }
                    }
                }
            }
        }
    }
}
