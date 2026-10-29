// SRow — 设置风格的列表行: 图标 + 文字 + 右箭头, 底部细分隔线
import QtQuick
import QtQuick.Layouts

Item {
    id: row
    property url iconSource
    property string text: ""
    property string detail: ""
    property bool showArrow: true
    property var th: null          // 外壳传 space.th (主题); 不传就是默认样子
    // 纯展示的行 (比如版本号): 不接点击, 也不给按下的灰底 —— 有反馈却没反应最让人困惑
    property bool interactive: true
    property real ui: 1            // 字号/尺寸缩放 (外壳传 space.fontScale)
    signal clicked()

    implicitHeight: Math.round(96 * ui)
    Rectangle { anchors.fill: parent; color: (row.interactive && mouse.pressed) ? (th ? th.press : "#eeeeee") : "transparent" }
    RowLayout {
        anchors.fill: parent
        anchors.leftMargin: 16
        anchors.rightMargin: 16
        spacing: 20
        Image {
            visible: row.iconSource.toString() !== ""
            Layout.preferredWidth: Math.round(36 * row.ui)
            Layout.preferredHeight: Math.round(36 * row.ui)
            source: row.iconSource
            fillMode: Image.PreserveAspectFit
            sourceSize.width: Math.round(36 * row.ui)
            sourceSize.height: Math.round(36 * row.ui)
        }
        Text {
            Layout.fillWidth: true
            text: row.text
            font.pixelSize: Math.round(28 * row.ui)
            elide: Text.ElideRight
        }
        Text {
            visible: row.detail !== ""
            text: row.detail
            font.pixelSize: Math.round(22 * row.ui)
            color: row.th ? row.th.text3 : "#000000"
        }
        Image {
            visible: row.showArrow && row.interactive
            Layout.preferredWidth: 24
            Layout.preferredHeight: 24
            source: Qt.resolvedUrl("icons/caret-right.svg")
            fillMode: Image.PreserveAspectFit
            sourceSize.width: 24
            sourceSize.height: 24
        }
    }
    Rectangle { anchors.left: parent.left; anchors.right: parent.right; anchors.bottom: parent.bottom; height: 1; color: row.th ? row.th.lineSoft : "#C4BFB6" }
    MouseArea { id: mouse; anchors.fill: parent; enabled: row.interactive; onClicked: row.clicked() }
}
