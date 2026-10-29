// SButton — 墨水屏风格按钮: 主按钮实心黑, 次按钮描边
import QtQuick

Rectangle {
    id: btn
    property string text: ""
    property bool primary: false
    property var th: null          // 外壳传 space.th (主题); 不传就是默认样子
    property real ui: 1            // 字号/尺寸缩放 (外壳传 space.fontScale)
    signal clicked()

    implicitWidth: Math.max(Math.round(160 * ui), label.implicitWidth + Math.round(48 * ui))
    implicitHeight: Math.round(64 * ui)
    radius: 8
    color: !btn.enabled ? (th ? th.press : "#eeeeee")
         : btn.primary ? (mouse.pressed ? (th ? th.text2 : "#000000") : (th ? th.primary : "#000000"))
         : (mouse.pressed ? (th ? th.press : "#eeeeee") : "transparent")
    border.color: btn.primary ? "transparent" : (th ? th.text : "#000000")
    border.width: btn.primary ? 0 : 1

    Text {
        id: label
        anchors.centerIn: parent
        text: btn.text
        font.pixelSize: Math.round(24 * btn.ui)
        color: !btn.enabled ? (th ? th.text3 : "#000000") : (btn.primary ? (th ? th.primaryText : "#ffffff") : (th ? th.text : "#000000"))
    }
    MouseArea {
        id: mouse
        anchors.fill: parent
        enabled: btn.enabled
        onClicked: btn.clicked()
    }
}
