// SClock — 首页时钟小组件 (外壳自带, 不依赖网络)
import QtQuick

Rectangle {
    id: clock
    property date now: new Date()
    property var th: null          // 外壳传 space.th (主题); 不传就是默认样子
    border.color: th ? th.line : "#B4AFA6"
    border.width: th ? th.border : 1
    radius: th ? Math.max(0, th.radius - 4) : 8
    color: "transparent"

    Timer {
        interval: 1000
        running: clock.visible
        repeat: true
        onTriggered: clock.now = new Date()
    }
    Column {
        anchors.centerIn: parent
        spacing: 6
        Text {
            anchors.horizontalCenter: parent.horizontalCenter
            text: Qt.formatTime(clock.now, "HH:mm")
            font.pixelSize: 96
            font.weight: Font.Light
        }
        Text {
            anchors.horizontalCenter: parent.horizontalCenter
            text: clock.now.toLocaleDateString(Qt.locale("zh_CN"), "yyyy年M月d日 dddd")
            font.pixelSize: 26
            color: clock.th ? clock.th.text2 : "#000000"
        }
    }
}
