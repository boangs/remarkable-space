// Space.qml — 「空间 (SPACE)」启动台外壳
//
// 职责只有三件事, 别往这里塞功能:
//   1. 首页与标签页: 空间 (小组件 + 我的应用) / 应用 (全部) / 发现 (商店) / 设置 (设置类应用列表)
//      数据全部来自 upload-server 的 /space/* (注册表、商店索引), 外壳不认任何具体应用
//   2. 打开/关闭应用: 按注册表里的 file:// 入口动态创建组件, 退出即销毁; 声明了后台的先拉起后台
//   3. 给应用一个稳定的 API 对象 (space): 基址、目录、标题/边框控制、返回拦截、HTTP 帮手
import QtQuick
import QtQuick.Layouts
import device.ui.controls
// kit 按相对路径引入: 自更新后外壳从 rel/<版本>/shell 加载, ../kit 正好是同版本的 kit
import "../kit" as Kit

Rectangle {
    id: space
    objectName: "_rmkitSpace"
    anchors.fill: parent
    color: space.th.bg
    visible: true
    z: 99999

    // 触摸拦截层 (必须是第一个子项, 压在所有内容底下)。
    // 外壳挂在 MainView 上, 底下就是 xochitl 自己那个全屏 MouseArea + MultiPointTouchArea
    // (文件库、侧栏都在里面)。Rectangle 本身不收触摸, 点在空白处 (标题行、边距、应用自己
    // 没铺 MouseArea 的地方) 的手指会穿过外壳落到文件库格子上, 悄悄打开一本书, 被外壳
    // 盖着看不见, 退出空间才露出来 —— 用户看到的就是"退出后莫名打开一本不是上次看的书"。
    // 两层都要: MouseArea 接合成鼠标 (第一指 / 笔), MultiPointTouchArea 接多指触摸。
    MouseArea { anchors.fill: parent; onPressed: (mouse) => { mouse.accepted = true } }
    MultiPointTouchArea { anchors.fill: parent; mouseEnabled: false }

    // 2: 文件库桥 (/space/library/*: 热导入、建文件夹/笔记本, 外壳调 requestLoadEntry)。
    //    用到它的应用 (如手写画布 lib/ink) 在 manifest 里写 min_shell: 2
    readonly property int shellVersion: 3
    readonly property string kitDir: String(Qt.resolvedUrl("../kit")).replace(/\/$/, "")   // 同上, 跟外壳同版本
    property string baseUrl: "http://127.0.0.1:8080"
    // ─── 尺寸基准: 不写死任何机型 ───
    // 以 Paper Pro Move 竖屏短边 954px 为 1.0; Paper Pro (1620x2160) 竖屏约 1.7。横屏时短边不变, 版式靠宽度自适应。
    // 两台机器 DPI 接近 (229 / 264), 所以字号只温和放大 (fs), 边距/卡片高度按 unit 放大, 列数按内容宽度算。
    readonly property real unit: Math.max(0.6, Math.min(width, height) / 954)
    // 系统剪贴板单例, 注入库经侧栏入口递进来 (空间组件里直接访问 Clipboard 是 ReferenceError); 贴纸用
    property var rmkitClip: null
    // 纵向另算一套。两台机器高宽比差很多 —— Move 954x1696 (1:1.78), Paper Pro 1620x2160
    // (1:1.33)。尺寸全跟着短边走的话, Paper Pro 上宽度放大 1.70 倍而可用高度只有 1.27 倍,
    // 首页纵向超出约 230px, 要滚一截才看得全。
    // 所以卡片高度、行距、上下留白改用 vpx: 元素该多大还多大 (字号图标仍按 unit), 只压纵向。
    // 下限 0.75 是给横屏留的 —— 横屏高度骤减, 再按比例压就没法看, 那种情况让它滚。
    readonly property real vunit: Math.max(0.75, Math.min(unit, height / 1696))
    readonly property real fs: 1 + (unit - 1) * 0.35
    readonly property bool largeScreen: unit > 1.4
    readonly property int margin: Math.round(64 * unit)
    function px(n) { return Math.round(n * unit) }      // 版式尺寸 (横向与元素)
    function vpx(n) { return Math.round(n * vunit) }   // 纵向尺寸: 卡片高度、行距、上下留白
    function fpx(n) { return Math.round(n * fs) }       // 字号
    function cols(contentWidth, minTile) { return Math.max(2, Math.floor(contentWidth / (minTile * fs))) }

    property string tab: "home"       // home / apps / discover / settings

    // ─── 刷新波形档 (整个空间统一) ───────────────────────────────────
    //   ui      = 原厂 MainView 那档: 快、有彩色、干净, 但色阶少
    //   content = 文档那档: 完整灰阶 + 彩色, 最细腻, 但慢, 进入时会留一层淡残影
    // 外壳整屏挂一个常驻标记, 几何零变化 (EPScreenModeMap 每变一次几何就整屏重合成),
    // 应用跑在 appHost 里自然继承, 所以应用侧不要再自己挂。
    property string screenMode: "ui"
    property var _screenModeItem: null
    // 刷新档位 (从慢到快)。值存进外壳配置, 对应 xochitl 的 ScreenModeItem 波形:
    //   content   高清: 灰阶、颜色最准, 最慢, 会闪
    //   ui        快刷 (默认)
    //   animation 流畅: 再快一档, 灰阶和颜色更粗
    //   mono      极速: 只有黑白两色, 最快; 照片、彩色图标会变成黑白
    readonly property var screenModes: ["content", "ui", "animation", "mono"]
    property bool screenModeOpen: false   // 设置页档位列表是否展开
    // 选档位: 必须放在外壳自己的函数里做 —— 收起列表会当场销毁被点的那一行,
    // 写在行的 onClicked 里的话, 收起之后的语句找不到 space, 配置没存上,
    // 下次重读配置 (开关应用时) 又变回原来的档位
    function chooseScreenMode(m) {
        screenMode = m
        saveShellCfg()
        screenModeOpen = false
    }
    function screenModeEnum(m) {
        return ({ content: "Content", animation: "Animation", mono: "Mono" })[m] || "UI"
    }
    function screenModeLabel(m) {
        return space.st(({ content: "screenmode_hd", animation: "screenmode_anim", mono: "screenmode_mono" })[m] || "screenmode_fast")
    }
    function applyScreenMode() {
        if (space._screenModeItem) { space._screenModeItem.destroy(); space._screenModeItem = null }
        try {
            space._screenModeItem = Qt.createQmlObject(
                'import QtQuick; import xofm.libs.epaper; ScreenModeItem { anchors.fill: parent; mode: ScreenModeItem.'
                + space.screenModeEnum(space.screenMode) + ' }',
                space, "spaceScreenMode")
        } catch (e) {
            console.warn("[space] ScreenModeItem 不可用, 沿用默认刷新: " + e)
        }
    }
    onScreenModeChanged: space.applyScreenMode()
    // 外壳自己的设置存在应用数据区的保留 id "shell" 下 (后端 id 正则不允许下划线开头)
    function loadShellCfg() {
        request("GET", baseUrl + "/space/apps/shell/data/config", null, function (st, r) {
            if (st === 200 && r && r.screenMode && space.screenModes.indexOf(r.screenMode) >= 0) space.screenMode = r.screenMode
            else space.applyScreenMode()
            if (st === 200 && r && r.hero !== undefined) space.heroChoice = r.hero
            if (st === 200 && r && r.timezone) space.tzPref = r.timezone
        })
        loadHeroList()
    }
    function saveShellCfg(cb) {
        request("PUT", baseUrl + "/space/apps/shell/data/config",
                { screenMode: space.screenMode, hero: space.heroChoice, lang: space.langPref,
                  timezone: space.tzPref }, cb || null)
    }

    // 首页顶部那张大卡放什么:
    //   ""      跟着装了什么走 —— 有天气就天气, 没有就内置那幅山水 (默认)
    //   "none"  就要内置山水, 哪怕装着天气
    //   其它    用户自己传的某张图的文件名
    property string heroChoice: ""
    property var heroImages: []
    function loadHeroList() {
        request("GET", baseUrl + "/hero", null, function (st, r) {
            if (st === 200 && r) space.heroImages = r
        })
    }
    // AppLoad 自己会往侧栏插一个入口。空间里已经能直接启动那些应用了
    // (launch.type=appload 走 AppLoadLauncher.launchApplication), 侧栏再放一个
    // 等于同一件事两个入口, 还把"从哪进"这件事搞乱。
    //
    // 它是 qmldiff 注入的, 带 objectName="openAppLoad" —— 这是唯一稳定的抓手:
    // id 在运行时拿不到, 按类型/位置找则会随固件改版漂移。
    //
    // 时机: 侧栏是懒加载的, 刚启动时还不存在。但用户是从侧栏点进空间的, 所以
    // 空间一显示就说明侧栏已经建好了 —— 在这里做最稳, 不用轮询也不用 Timer
    // (运行时注入的组件里 Timer 本来就不触发)。
    property bool apploadEntryHidden: false
    function hideApploadEntry() {
        if (apploadEntryHidden) return
        var root = space
        while (root.parent) root = root.parent
        var target = findByObjectName(root, "openAppLoad")
        if (target) {
            target.visible = false
            apploadEntryHidden = true
        }
    }
    function findByObjectName(node, name) {
        if (!node) return null
        if (node.objectName === name) return node
        var kids = node.children
        if (!kids) return null
        for (var i = 0; i < kids.length; i++) {
            var r = findByObjectName(kids[i], name)
            if (r) return r
        }
        return null
    }

    // 这台设备在应用商店那边的身份。买应用时用户要把它抄给我们, 所以设置里得显示。
    property string deviceCode: ""
    function loadDevice() {
        request("GET", baseUrl + "/space/device", null, function (st, r) {
            if (st === 200 && r) space.deviceCode = r.code || ""
        })
    }

    // 商店里买不到的那些 (服务端标了 locked = 这台设备还没有授权)。
    // 下载地址由服务端按设备发, 没授权就根本没有 zip —— 所以这里不是"点了没反应",
    // 而是压根不该让它看起来能装。
    function buyPrompt(app) {
        var lines = [space.st("buy_not_purchased", { name: app.name || app.id })]
        if (app.price) lines.push(space.st("buy_price", { price: app.price }))
        lines.push("")
        lines.push(space.st("buy_devicecode", { code: space.deviceCode !== "" ? space.deviceCode : space.st("devicecode_unknown") }))
        lines.push(app.buy ? app.buy : space.st("buy_hint_1"))
        lines.push(space.st("buy_hint_2"))
        space.ask(lines.join("\n"), null)
    }

    function heroWidgetEnabled() { return space.heroChoice === "" }
    // 封面: 用户自己设的 > 主题自带的 > 默认山水
    function heroImageUrl() {
        if (space.heroChoice !== "" && space.heroChoice !== "none")
            return space.baseUrl + "/hero/" + encodeURIComponent(space.heroChoice)
        if (space.th.hero) return space.th.hero
        return space.kitDir + "/images/hero-default.jpg"
    }

    // ─── 主题 (GET /space/theme, 见 internal/space/theme.go) ────────────
    // 内置值就是默认主题 (= v1.0 的样子): 后台没起来或主题坏了照样画得出来。
    // 墨水屏不做深色主题: 没写颜色的 Text 默认黑字, 主题只能在浅底上换。
    readonly property var thDefault: ({
        bg: "white", card: "white", fill: "#f0f0f0", press: "#eeeeee",
        line: "#B4AFA6", lineSoft: "#C4BFB6", lineFaint: "#E2DED6",
        text: "#000000", text2: "#262626", textBody: "#1A1A18", text3: "#26251F", textNote: "#1F1E1B",
        primary: "#000000", primaryText: "white",
        radius: 12, border: 1, hero: "", icons: ({}), slots: ({}), id: "default"
    })
    property var th: thDefault
    function loadTheme() {
        request("GET", baseUrl + "/space/theme", null, function (st, r) {
            if (st !== 200 || !r) return
            var t = Object.assign({}, space.thDefault)
            var c = r.colors || {}
            for (var k in c) if (t[k] !== undefined && typeof c[k] === "string") t[k] = c[k]
            if (typeof r.radius === "number") t.radius = r.radius
            if (typeof r.border === "number") t.border = r.border
            t.hero = r.hero || ""
            t.icons = r.icons || {}
            t.slots = r.slots || {}
            t.id = r.id || "default"
            space.th = t
        })
    }
    // kit 图标: 主题换了的用主题的
    function icon(name) { return (space.th.icons && space.th.icons[name]) || (space.kitDir + "/icons/" + name + ".svg") }
    property var apps: []
    property string listError: ""
    property var storeApps: []
    // 商店分三栏: apps 应用 / themes 主题与贴纸包 / system 空间本体、系统补丁与 AI 等系统级应用 (索引里的 section 字段, 由应用仓库 pack.sh 按 manifest 推出)
    property string storeSection: "apps"
    readonly property var storeSections: ["apps", "themes", "system"]
    function storeSectionOf(a) {
        if (a.section) return a.section
        // 旧索引没有 section 时按已知字段兜底
        if (a.kind === "space" || a.category === "system") return "system"
        if (a.id === "stickers" || String(a.id || "").indexOf("theme-") === 0) return "themes"
        return "apps"
    }
    function storeList() {
        var sec = storeSection
        return storeApps.filter(function (a) { return space.storeSectionOf(a) === sec })
    }
    property var recentDoc: null      // 最近在读的文档 (/space/recent, 取自 xochitl 元数据)
    property string storeError: ""
    property bool storeLoaded: false
    property int tzOffset: 60             // local zone offset in minutes, set by the backend; the device clock is UTC
    property string tzName: "Europe/Berlin" // zone name decided by the backend (must match defaultTimezone in timezone.go)
    property string tzPref: ""            // 用户在设置里选的 (shell/config.json 的 timezone); 空 = 默认北京时间
    property var tzList: []               // 设置里可选的时区 (GET /space/timezones)
    property bool tzOpen: false           // 设置页时区列表是否展开
    function tzLabel(name) {
        for (var i = 0; i < tzList.length; i++)
            if (tzList[i].name === name) return lang === "zh-CN" ? tzList[i].zh : tzList[i].en
        return name
    }
    function tzOffsetText(min) {
        var sign = min < 0 ? "-" : "+", a = Math.abs(min)
        var h = Math.floor(a / 60), m = a % 60
        return "UTC" + sign + h + (m ? ":" + (m < 10 ? "0" : "") + m : "")
    }
    function loadTimezones() {
        request("GET", baseUrl + "/space/timezones", null, function (st, r) { if (st === 200 && r) space.tzList = r })
    }
    // 选时区: 写进外壳配置, 写完重读 /space/apps 拿新的 tzOffset (时钟、日期、小组件都跟着变)
    function chooseTimezone(name) {
        tzPref = name
        tzOpen = false
        saveShellCfg(function () { space.reload() })
    }
    property bool devMode: false          // 后端 .dev 标记: 打开应用绕过 QML 组件缓存
    property date now: new Date()
    // 本地时间 = 当前时刻按 tzOffset 平移后, 用进程时区 (UTC) 的表示来显示; 若进程时区不是 UTC 也能纠正
    function localNow() { return new Date(now.getTime() + (tzOffset + now.getTimezoneOffset()) * 60000) }

    property var current: null        // 正在打开的应用 (注册表条目)
    property var currentItem: null    // 应用根 Item
    property string appError: ""      // 应用加载失败原因 (给开发者看)
    property string appTitle: ""
    property bool chrome: true        // false = 应用全屏 (自己画导航)
    property string toastText: ""
    property string busyText: ""
    property var confirmApp: null
    // 通用确认框。原来只为"重启进 Android"这类 launch.confirm 服务, 删应用也得问一句,
    // 所以拆出一条独立的: 文案 + 一个点space.st("ok")才跑的回调。
    property string askText: ""
    property var askAction: null
    // action 为 null 时就是一条提示 (只有space.st("got_it")), 不是在问要不要做什么。
    property bool askNotice: false
    function ask(text, action) { askText = text; askAction = action; askNotice = !action }
    function askDone(ok) {
        var fn = askAction
        askText = ""; askAction = null; askNotice = false
        if (ok && fn) fn()
    }

    // ─── 给应用的 API ─────────────────────────────────────────────────
    QtObject {
        id: api
        readonly property int shellVersion: space.shellVersion
        readonly property string baseUrl: space.baseUrl
        readonly property string kitDir: space.kitDir
        readonly property bool largeScreen: space.largeScreen
        readonly property string screenMode: space.screenMode   // content / ui / animation / mono, 整个空间统一, 应用别自己挂
        readonly property real unit: space.unit           // 版式缩放基准 (Move 竖屏 = 1)
        // 纵向另算一套: Paper Pro 宽是 Move 的 1.70 倍而高只有 1.27 倍, 纵向尺寸
        // 再跟着 unit 走就会把一屏撑爆。卡片高度、上下留白、纵向间距用它。
        readonly property real vunit: space.vunit
        readonly property real fontScale: space.fs        // 字号缩放基准
        readonly property real screenWidth: space.width
        readonly property real screenHeight: space.height
        readonly property var theme: space.th                 // 当前主题的颜色/圆角/描边 (键见 thDefault)
        function icon(name) { return space.icon(name) }       // kit 图标 (主题换了的用主题的)
        function reloadTheme() { space.loadTheme() }          // 个性化里换了主题后叫一下
        function openStore() { space.closeApp(); space.tab = "discover" }   // 退出应用, 打开商店页
        property string appId: ""
        property string appDir: ""       // 应用目录 (绝对路径)
        property string dataDir: ""      // 应用私有数据目录 (卸载不删)
        property string serviceUrl: ""   // 应用自带后台的 http://127.0.0.1:<port>, 没有则空
        property string title: ""        // 改它 = 改外壳大标题
        property bool chrome: true       // false = 外壳隐藏导航栏和标题, 应用全屏
        property var onBack: null        // function(): 返回 true 表示应用自己消费了返回键

        onTitleChanged: space.appTitle = title
        onChromeChanged: space.chrome = chrome

        function exit() { space.closeApp() }              // 退出应用回到原标签页
        function closeSpace() { space.close() }           // 连启动台一起关
        function toast(msg) { space.showToast(msg) }
        function openApp(id) { space.openById(id) }       // 应用间跳转 (如小组件点开自己的应用)
        function request(method, url, body, cb) { space.request(method, url, body, cb) }
        // 应用把 main.qml 拆成多个文件时, 子文件的 URL 也走这里: 和入口同一个 rev 令牌,
        // 开发模式下改了会自动失效, 没改就命中缓存 (别自己拼 Date.now(), 那会每次都重编)
        function bust(url) { return space.bust(url, space.current ? space.current.rev : 0) }
        function get(path, cb) { space.request("GET", space.baseUrl + path, null, cb) }
        function post(path, body, cb) { space.request("POST", space.baseUrl + path, body, cb) }
        function svcGet(path, cb) { space.request("GET", serviceUrl + path, null, cb) }
        function svcPost(path, body, cb) { space.request("POST", serviceUrl + path, body, cb) }
        function localNow() { return space.localNow() }   // 按设备时区 (见 /space/apps 的 tzOffset) 的当前时间
        // Current UI language (en / fr / de / zh-CN). Follows the reMarkable system setting; apps need not detect it.
        readonly property string lang: space.lang
        // 查本应用 i18n/<lang>.json 的词条; 缺词回落 en, 再回落 key 本身。
        // 不要用 qsTr —— 注入进来的 QML 查不到 Qt 的翻译表 (已验证), tr 走自己的词典。
        function tr(key, args) { return space.tr(key, args) }
        // 小组件里用这个 —— space.trApp(app.id, "key")。首页上通常没有挂载应用,
        // 用 tr 会查到空词典 (露出 key) 或别的应用的词典。
        function trApp(appId, key, args) { return space.trApp(appId, key, args) }
        // 应用数据: 存到 dataDir/<key>.json (纯 QML 应用的持久化); 小组件要传自己的 app.id
        function dataGet(id, key, cb) { space.request("GET", space.baseUrl + "/space/apps/" + id + "/data/" + key, null, cb) }
        function dataPut(id, key, obj, cb) { space.request("PUT", space.baseUrl + "/space/apps/" + id + "/data/" + key, obj, cb) }
    }

    // ─── 逻辑 ─────────────────────────────────────────────────────────
    function request(method, url, body, cb) {
        var x = new XMLHttpRequest()
        x.onreadystatechange = function() {
            if (x.readyState !== 4) return
            // 每次打开空间外壳都重建一次, 旧实例发出的请求回来时它已经销毁 (space 成了 null),
            // 回调里再碰 space / 小组件的函数只会报 "of null" / "is not defined"。没人要这个结果了, 丢掉。
            if (!space) return
            var r = null
            try { r = JSON.parse(x.responseText) } catch (e) { r = null }
            // file:// 读成功时 XHR 的 status 是 0 而不是 200 —— 本地文件没有 HTTP 状态行。
            // 应用读自带的 json 资源 (如天气的 phrases.json) 走的就是 file://, 调用方
            // 一律按 200 判断, 不折算的话那些资源永远读不到, 还只会静默走兜底分支。
            var st = x.status
            if (st === 0 && url.indexOf("file:") === 0 && x.responseText) st = 200
            if (cb) cb(st, r)
        }
        x.open(method, url)
        var hasBody = body !== null && body !== undefined
        if (hasBody) x.setRequestHeader("Content-Type", "application/json")
        x.send(hasBody ? JSON.stringify(body) : null)
    }

    // ─── 语言 ─────────────────────────────────────────────────────────
    // 语言只由后端定 (internal/space/lang.go): 系统正在用的 .qm → 空间里选的 → xochitl.conf
    // → 环境变量。外壳不再自己合并 —— 商店、应用名也是后端按同一个结果换的, 两处各算各的
    // 迟早对不上。外壳只在首帧前同步猜一次 (bootLang), 后端结果一到就以它为准。
    property string lang: "en"
    property string langPref: ""   // 用户在设置里显式选的; 空 = 跟着后端推断走
    property string langSystem: "" // xochitl 正在用的界面语言 (看它映射的 .qm); 非空就跟它走, 不给选
    property var dict: ({})        // 当前应用的词典
    property var dictEn: ({})      // 应用的 en 词典, 缺词时回落用
    readonly property string shellDir: "file:///home/root/rmkit-cn/space/shell"

    // 词典必须在首帧之前就位 —— 异步读会让界面先闪一下 key (tab_home) 再变成文字。
    // 都是本地 file:// 的几 KB JSON, 同步读几毫秒, 换一个不闪的首帧值得。
    function readJsonSync(url) {
        var x = new XMLHttpRequest()
        try {
            x.open("GET", url, false)   // false = 同步
            x.send(null)
            return JSON.parse(x.responseText)
        } catch (e) {
            return null
        }
    }

    // 开机语言: 后端那套推断是异步的, 来不及赶上首帧, 这里同步猜一次。
    // 外壳就在 xochitl 进程里, /proc/self/maps 里映射着哪个 reMarkable_xx.qm 就是
    // 系统当前的界面语言 (和后端 SystemLang 同一个办法); 读不到再认用户存的偏好,
    // 都没有按中文。
    function sysLangSync() {
        var x = new XMLHttpRequest()
        try {
            x.open("GET", "file:///proc/self/maps", false)
            x.send(null)
            var m = (x.responseText || "").match(/\/translations\/reMarkable_([A-Za-z_-]+)\.qm/)
            return m ? normLang(m[1]) : ""
        } catch (e) {
            return ""
        }
    }
    readonly property string bootLang: {
        var l = sysLangSync()
        if (l) return l
        var cfg = readJsonSync("file:///home/root/.local/share/rmkit-cn/space/data/shell/config.json")
        l = normLang(cfg && cfg.lang ? cfg.lang : "")
        return l || "en"
    }
    property var shellDict: readJsonSync(shellDir + "/i18n/" + bootLang + ".json") || ({})
    property var shellDictEn: bootLang === "en" ? shellDict
                                                : (readJsonSync(shellDir + "/i18n/en.json") || ({}))

    // zh_CN / zh-Hans / zh → zh-CN; 认不出的回落 en。和后端 NormalizeLang 保持一致。
    function normLang(s) {
        if (!s) return ""
        s = ("" + s).trim()
        var cut = s.search(/[.@]/)
        if (cut > 0) s = s.substring(0, cut)
        s = s.replace(/_/g, "-")
        var low = s.toLowerCase()
        if (low.indexOf("zh") === 0) return "zh-CN"
        if (low === "en" || low.indexOf("en-") === 0) return "en"
        if (low === "fr" || low.indexOf("fr-") === 0) return "fr"
        if (low === "de" || low.indexOf("de-") === 0) return "de"
        return ""
    }

    function setLang(code) {
        var n = normLang(code)
        lang = n || "en"
    }

    // Language picker (only shown when the system language cannot be read).
    // Keep in sync with SupportedLangs in internal/space/lang.go.
    readonly property var chooseableLangs: ["en", "fr", "de", "zh-CN"]
    function langNativeName(code) {
        var names = { "en": "English", "fr": "Fran\u00e7ais", "de": "Deutsch", "zh-CN": "\u4e2d\u6587\uff08\u7b80\u4f53\uff09" }
        return names[code] || code
    }
    function nextLang(code) {
        var i = chooseableLangs.indexOf(code)
        return chooseableLangs[(i + 1) % chooseableLangs.length]   // unknown code (-1) -> first entry
    }
    // Qt locale used to format dates in the home card.
    function qtLocaleName() {
        var m = { "zh-CN": "zh_CN", "fr": "fr_FR", "de": "de_DE" }
        return m[lang] || "en_US"
    }

    // 设置里切语言。写进 shell/config.json 的 lang 键, 顺手把当前应用的词典重读一遍,
    // 这样不用退出空间再进去。应用名 (扫码上传/个性化…) 是后端列表接口按语言换好的,
    // 商店列表同理。所以存完还得重拉一次 —— 必须等存完, 否则后端还按旧语言给名字。
    function chooseLang(code) {
        langPref = code
        setLang(code)
        saveShellCfg(function() { reload(); if (storeLoaded) loadStore() })
        loadShellDict()
        if (current) loadDict(current)
    }

    // 外壳自己的文案。args 传对象做占位替换: st("downloading", {name: "天气"})
    // 词典里写 "正在下载 {name}…" / "Downloading {name}…" —— 占位符位置随语言走。
    // 查词: 先当前语言再 en 再 key 本身。args.n === 1 时先试 <key>_one ——
    // 英文单复数不同 ("1 album" / "2 albums"), 中文词典不用写 _one, 找不到就落回原 key。
    // Which numbers use the "<key>_one" (singular) form? English and German: only 1.
    // French: 0 and 1 ("0 album", "1 album", "2 albums").
    function isSingular(n) { return n === 1 || (lang === "fr" && n === 0) }
    function lookup(d, dEn, key, args) {
        if (args && isSingular(args.n)) {
            var one = d[key + "_one"]
            if (one === undefined && d[key] === undefined) one = dEn[key + "_one"]
            if (one !== undefined) return one
        }
        var v = d[key]
        if (v === undefined) v = dEn[key]
        return v === undefined ? key : v
    }

    function st(key, args) {
        var v = lookup(shellDict, shellDictEn, key, args)
        if (args !== undefined && args !== null) {
            for (var k in args) v = v.split("{" + k + "}").join(args[k])
        }
        return v
    }

    function loadShellDict() {
        var base = shellDir + "/i18n/"
        shellDictEn = readJsonSync(base + "en.json") || ({})
        shellDict = (lang === "en") ? shellDictEn : (readJsonSync(base + lang + ".json") || ({}))
    }

    // 查应用词典。缺词先回落 en, 再回落 key 本身 —— 永远不会显示成空白。
    function tr(key, args) {
        if (key === undefined || key === null) return ""
        var v = lookup(dict, dictEn, key, args)
        if (args !== undefined && args !== null) {
            for (var k in args) v = v.split("{" + k + "}").join(args[k])
        }
        return v
    }

    // 载入某个应用目录下的词典。两份都读: 当前语言 + en, 后者用于缺词回落。
    // 读不到就留空字典, tr() 会把 key 原样显示出来, 不至于白屏。
    // 同步读, 理由同 readJsonSync 上面那段: 应用首帧不该闪 key。
    // 这是在 mount 里、createComponent 之前调用的, 读完才开始实例化界面。
    // 每个应用一份词典, 按 id + 版本 + 语言缓存。小组件和应用共用 api 对象, 但首页上
    // 显示小组件时往往没有挂载任何应用 —— 只按"当前应用"查词, 小组件会露出 key,
    // 或者查到上一个打开的应用的词典。所以小组件要按自己的 app.id 查 (见 trApp)。
    property var dictCache: ({})
    function appDict(app) {
        if (!app || !app.dir) return { cur: ({}), en: ({}) }
        var ck = app.id + "@" + (app.rev || 0) + "@" + lang
        var hit = dictCache[ck]
        if (hit) return hit
        var base = "file://" + app.dir + "/i18n/"
        var en = readJsonSync(base + "en.json") || ({})
        var d = { en: en, cur: (lang === "en") ? en : (readJsonSync(base + lang + ".json") || ({})) }
        dictCache[ck] = d
        return d
    }
    function loadDict(app) {
        var d = appDict(app)
        dict = d.cur; dictEn = d.en
    }
    // 小组件用: 按应用 id 查它自己的词典, 不受当前挂载的是哪个应用影响
    function trApp(appId, key, args) {
        var app = null
        for (var i = 0; i < apps.length; i++) if (apps[i].id === appId) { app = apps[i]; break }
        var d = appDict(app)
        var v = lookup(d.cur, d.en, key, args)
        if (args !== undefined && args !== null) {
            for (var k in args) v = v.split("{" + k + "}").join(args[k])
        }
        return v
    }

    function reload() {
        listError = ""
        request("GET", baseUrl + "/space/apps", null, function(st, r) {
            if (st !== 200 || !r) { listError = space.st("backend_down", { code: st }); return }
            // 先设开发模式与时区: apps 一赋值就会触发小组件挂载, 那时 devMode 必须已经就位,
            // 否则第一次加载不带免缓存后缀, Qt 会把旧版编译结果缓存住, 之后改 QML 都不生效
            if (r.tzOffset !== undefined) tzOffset = r.tzOffset
            if (r.timezone) tzName = r.timezone
            devMode = !!r.dev
            langPref = r.langPref || ""
            langSystem = normLang(r.langSystem || "")
            setLang(r.lang || "en")
            // bootLang 阶段已经同步读过一次了; 只有最终定的语言和开机猜的不一样才重读
            if (lang !== bootLang) loadShellDict()
            console.warn("SPACE-LANG: boot=" + JSON.stringify(bootLang) + " system=" + JSON.stringify(langSystem) + " pref=" + JSON.stringify(langPref) + " ⇒ " + lang)
            apps = r.apps || []
            if (!space.upd && r.spaceVersion) space.upd = { current: r.spaceVersion }
        })
        loadRecent()
    }

    // 开发模式下给 file:// 加查询串绕开缓存。令牌用的是应用目录的最新改动时间 (后端的 rev),
    // 不是 Date.now() —— 用时钟的话每次打开都是新 URL, 引擎的类型缓存和磁盘缓存同时落空,
    // 每开一次就把整个 main.qml 重编一遍 (RMPPM 上实测 135KB 要 5.2 秒, 占打开耗时的 94%)。
    // 按 rev 之后: 文件没动就一直命中缓存, 一改文件 rev 变了, 下次打开自动重编一次。
    // 只对 file:// 生效: 内置应用的图标是 qrc:/ark/icons/..., 给 qrc URL 加查询串会加载失败
    function bust(url, rev) {
        if (!devMode || !url || url.indexOf("file:") !== 0) return url
        return url + "?t=" + (rev || 0)
    }

    // 最近在读: 后端扫 xochitl 的 .metadata, 60 秒缓存, 这里只管取
    function loadRecent() {
        request("GET", baseUrl + "/space/recent", null, function(st, r) {
            recentDoc = (st === 200 && r && r.doc && r.doc.name) ? r.doc : null
        })
    }

    function loadStore(refresh) {
        storeError = ""
        request("GET", baseUrl + "/space/store" + (refresh ? "?refresh=1" : ""), null, function(st, r) {
            storeLoaded = true
            if (st !== 200 || !r) { storeError = (r && r.error) ? r.error : space.st("store_unreachable", { code: st }); storeApps = []; return }
            storeApps = (r.apps || []).filter(function (a) { return a.id !== "space" })   // 空间自己的新版本走 /space/update, 在「系统」栏置顶单独画
        })
    }

    // 比版本号: 按数字逐段比 ("1.6.13" > "1.2.0"), a 比 b 新返回正数。
    // 商店只在线上版本比本机新时才给「更新」—— 只比相不相等的话, 本机装了更新的
    // (开发版 / 直装) 也会显示「更新」, 一点就降级回旧版。
    // 带 "-" 预发布后缀的低于同号正式版 (和后端 VersionCmp 一致), 不然装了 v1.1.0-beta.2 的机器看不到 v1.1.0。
    function versionCmp(a, b) {
        var pa = String(a || "").split("-"), pb = String(b || "").split("-")
        var d = numCmp(pa[0], pb[0])
        if (d !== 0) return d
        if (pa.length === 1 && pb.length > 1) return 1
        if (pa.length > 1 && pb.length === 1) return -1
        return numCmp(pa.slice(1).join("-"), pb.slice(1).join("-"))
    }
    function numCmp(a, b) {
        var x = String(a || "").match(/\d+/g) || [], y = String(b || "").match(/\d+/g) || []
        for (var i = 0; i < Math.max(x.length, y.length); i++) {
            var d = (parseInt(x[i] || "0", 10)) - (parseInt(y[i] || "0", 10))
            if (d !== 0) return d
        }
        return 0
    }
    function installedVersion(id) {
        for (var i = 0; i < apps.length; i++) if (apps[i].id === id) return apps[i].version || "?"
        return ""
    }

    // 删除已装的应用。后端会连它的编译模块一起收走; 应用的数据目录保留 —— 误删之后
    // 重新装回来, 任务/歌单/进度都还在。内置应用后端会拒绝, 这里也不给删除按钮。
    function uninstallApp(app) {
        request("DELETE", baseUrl + "/space/apps/" + app.id, null, function (st, r) {
            if (st !== 200) {
                showToast((r && r.error) ? r.error : space.st("delete_failed", { code: st }))
                return
            }
            showToast(space.st("deleted", { name: app.name || app.id }))
            reload()
            loadStore(false)
        })
    }

    // 安装在后端后台跑, 这边轮询进度。下载几 MB 在设备的 Wi-Fi 上要十几秒, 同步等的话
    // 界面只能干等, 用户分不出是在下载还是卡住了。
    // 墨水屏不跑动画 —— 每次重绘都是实打实的屏幕刷新, 进度条滚起来既慢又闪, 给个百分比
    // 数字最省事; 轮询间隔 1.2 秒也是这个道理, 太密了屏幕一直在抖。
    function installFromStore(item) {
        busyText = space.st("downloading", { name: item.name })
        request("POST", baseUrl + "/space/apps/install-url",
                { url: item.zip, sha256: item.sha256 || "", id: item.id, name: item.name },
                function (st, r) {
                    if (st !== 200 && st !== 202) {
                        busyText = ""
                        showToast((r && r.error) ? r.error : space.st("install_failed_code", { code: st }))
                        return
                    }
                    installPoll.restart()
                })
    }

    Timer {
        id: installPoll
        interval: 1200
        repeat: true
        onTriggered: space.request("GET", space.baseUrl + "/space/apps/install-progress", null,
                                   function (st, p) {
            if (st !== 200 || !p) return
            // 只有终态才收摊。中间阶段不止"下载"和"安装"两种 (带本体的应用还要再下一次),
            // 原来按阶段名逐个匹配, 新增一个阶段就会掉到下面的收尾逻辑里 ——
            // 进度框闪一下就没, 装完也不刷新列表, 看着像安装失败了。
            if (p.phase !== "done" && p.phase !== "error") {
                if (p.phase === "installing") {
                    space.busyText = space.st("installing", { name: p.name })
                } else {
                    // 服务器没给 Content-Length 时只报已下多少, 别显示一个假的百分比
                    space.busyText = p.total > 0
                            ? space.st("downloading_pct", { name: p.name, pct: Math.round(p.got * 100 / p.total) })
                            : space.st("downloading_mb", { name: p.name, mb: (p.got / 1048576).toFixed(1) })
                }
                return
            }
            installPoll.stop()
            space.busyText = ""
            if (p.phase === "error") { space.showToast(p.error || space.st("install_failed")); return }
            if (p.phase === "done") {
                space.showToast(space.st("installed_toast", { name: p.name || space.st("app_fallback_name") }))
                space.reload()
            }
        })
    }

    function hasUi(a) { return !!(a.entryUrl || a.launch || (a.module && (a.module.main || a.module.widget)) || a.widgetUrl) }
    function filterApps(pred) {
        var out = []
        // 纯内容条目 (只有贴纸包 / 只有挂载组件, 没有界面也没有启动入口) 不是应用, 哪儿都不列
        for (var i = 0; i < apps.length; i++) if (space.hasUi(apps[i]) && pred(apps[i])) out.push(apps[i])
        return out
    }
    // 应用有没有首页小组件 —— 明文发布看 widgetUrl, 编译发布看 module.widget。
    // 四处筛选都走这里, 免得加了新发布形态就漏一处。
    function hasWidget(a) {
        return !!a && (!!a.widgetUrl || !!(a.module && a.module.widget))
    }

    function heroWidgetApp() {
        var l = filterApps(function(a) { return space.hasWidget(a) && a.widget_size === "hero" && !a.error })
        return l.length ? l[0] : null
    }
    function halfWidgetApps() {
        return filterApps(function(a) { return space.hasWidget(a) && a.widget_size !== "hero" && !a.error })
    }
    // space.st("app_fallback_name") = 非设置类 (设置类只出现在space.st("tab_settings")标签)
    // 系统补丁 (category=system) 哪里都不列: 状态只在「商店 → 系统」里看, 装过的点那一行打开
    function isSetting(a) { return a.category === "settings" }
    function isSystem(a) { return a.category === "system" }
    function userApps() { return filterApps(function(a) { return !isSetting(a) && !isSystem(a) }) }
    function homeApps() {
        var l = filterApps(function(a) { return !a.error && !isSetting(a) && !isSystem(a) })
        var n = homeGrid.columns * 2   // 首页只放两行, 其余在space.st("app_fallback_name")页
        return l.length > n ? l.slice(0, n) : l
    }
    function settingsApps() { return filterApps(isSetting) }
    // 声明了 widget_slot 的小组件由外壳指定位置 (如 "plan" → 今日计划那张卡)
    function slotWidgetApp(slot) {
        var l = filterApps(function(a) { return space.hasWidget(a) && a.widget_slot === slot && !a.error })
        return l.length ? l[0] : null
    }
    // 没指定位置的半宽小组件, 统一进「正在进行」
    function plainWidgetApps() {
        return filterApps(function(a) { return space.hasWidget(a) && a.widget_size !== "hero" && !a.widget_slot && !a.error })
    }

    function openById(id) {
        for (var i = 0; i < apps.length; i++) if (apps[i].id === id) { open(apps[i]); return }
        showToast(space.st("not_installed", { id: id }))
    }
    // 商店「系统」栏: 装过且带状态页的补丁, 点那一行打开 (它不在设置列表里)
    function installedWithUi(id) {
        for (var i = 0; i < apps.length; i++) if (apps[i].id === id) return space.hasUi(apps[i]) ? apps[i] : null
        return null
    }

    function open(app) {
        if (app.error) { showToast(app.error); return }
        // 有界面就挂界面, 没有才当外部程序。界面有两种形态: 明文的 entryUrl,
        // 或编译发布的 module.main —— 后者包里没有 .qml, 判断时漏掉它就会被误当成
        // "外部程序", 报space.st("app_no_entry")。
        if (!app.entryUrl && !(app.module && app.module.main)) { launch(app); return }
        // 开发模式下先把列表拉一遍再开: 免缓存令牌是应用目录的改动时间, 存在列表里。
        // 不重拉的话, 刚 scp 上去的 QML 用的还是进空间那一刻的旧令牌, URL 没变 → 引擎
        // 直接给缓存里的旧版, 人在设备上退出应用再进也没用, 只能退出整个空间重来。
        if (devMode && !app._fresh) {
            request("GET", baseUrl + "/space/apps", null, function(st, r) {
                if (st === 200 && r && r.apps) {
                    apps = r.apps
                    for (var i = 0; i < apps.length; ++i)
                        if (apps[i].id === app.id) {
                            var a = apps[i]
                            a._fresh = true      // 防止递归
                            space.open(a)
                            return
                        }
                }
                app._fresh = true
                space.open(app)
            })
            return
        }
        if (app.service) {
            busyText = space.st("starting_service", { name: app.name })
            request("POST", baseUrl + "/space/apps/" + app.id + "/service/start", null, function(st, r) {
                busyText = ""
                if (st !== 200) { showToast((r && r.error) ? r.error : space.st("service_failed", { code: st })); return }
                mount(app, (r && r.url) ? r.url : (app.serviceUrl || ""))
            })
            return
        }
        mount(app, "")
    }

    // 卸载当前应用: 销毁界面树 + 按需停后台。应用跳应用 (space.openApp) 与退出应用都走它。
    //
    // 原来只有 closeApp 做销毁, mount 直接覆盖 currentItem —— 旧应用那棵树还挂在
    // appHost 上继续显示 (音乐里点蓝牙, 两屏内容叠在一起), 而且 currentItem 一被覆盖
    // 就再没人能销毁它, 一直泄漏到 xochitl 退出。
    function unmount() {
        if (currentItem) { currentItem.destroy(); currentItem = null }
        // 「个性化」改的是外壳自己的配置 (首页插图), 退出来得重新读一遍才看得到效果
        loadShellCfg()
        var app = current
        current = null
        appError = ""
        api.onBack = null
        // keepalive 的后台不停 (音乐跳去蓝牙连耳机时还在放)
        if (app && app.service && !app.service.keepalive)
            request("POST", baseUrl + "/space/apps/" + app.id + "/service/stop", null, null)
    }

    // 为编译发布的应用造一个 Component。
    //
    // 编译之后包里没有 .qml 文件, createComponent 无从下手, 只能靠 import 模块名。
    // 包一层 Component 而不是直接 createQmlObject 出对象, 是为了和明文那条路接口一致 ——
    // 后面都走 createObject(appHost, {space: api}), 让 space 在对象构造时就位。应用的
    // 属性初始化 (如 dir: "file://" + space.appDir) 依赖这一点, 事后再赋值就晚了。
    function moduleComponent(app) {
        var uri = app.module.uri, type = app.module.main
        // 分步打点: 模块出问题时整个界面会卡住, 卡住前没有任何输出的话根本不知道
        // 停在哪一步 —— 是 import 解析、plugin 加载, 还是实例化。
        console.warn("[space] 模块 " + uri + "." + type + " 开始 import")
        try {
            // 用数组 join 拼多行 QML, 不写字符串里的换行转义 —— 之前那版把转义符写成了
            // 真实换行, JS 字符串字面量跨行是畸形的, 结果 Qt 解析时主线程 100% 空转、
            // 界面全卡死, 连 "import 完成" 都打不出来 (实验 2~5 全中)。
            // 第三个参数是解析这段 QML 的基准 URL, 必须合法; 原来传的 "module:<uri>"
            // 是自造 scheme, 一并换成外壳自己的 URL。
            var src = ['import QtQuick', 'import ' + uri,
                       'Component { ' + type + ' { } }'].join('\n')
            var r = Qt.createQmlObject(src, space, Qt.resolvedUrl("Space.qml"))
            console.warn("[space] 模块 " + uri + " import 完成, Component 已就绪")
            return r
        } catch (e) {
            // 最常见的是模块没装到 QML_IMPORT_PATH 指的目录, 或者 .so 架构不对
            return { status: Component.Error,
                     errorString: function () { return space.st("module_load_failed", { uri: uri, err: e }) } }
        }
    }

    function mount(app, serviceUrl) {
        unmount()
        api.appId = app.id
        api.appDir = app.dir
        api.dataDir = app.dataDir
        api.serviceUrl = serviceUrl
        api.onBack = null
        api.title = app.name
        api.chrome = true
        appTitle = app.name
        chrome = true
        appError = ""
        current = app
        // 两种发布形态:
        //   明文: entryUrl 指向应用目录里的 .qml, 直接 createComponent
        //   编译: manifest 声明 module, 包里只有 .so —— 用一段即时 QML 去 import 它。
        //        模块装在固定的 qmlmods 目录, 由 xochitl 的 QML_IMPORT_PATH 指过去。
        loadDict(app)   // 同步: 必须在 createComponent 之前读完, 否则应用首帧闪 key
        var c = app.module && app.module.main
                ? moduleComponent(app)
                : Qt.createComponent(bust(app.entryUrl, app.rev))
        if (c.status === Component.Error) {
            appError = c.errorString()
            console.log("[space] 应用 " + app.id + " 加载失败: " + appError)
            return
        }
        console.warn("[space] " + app.id + " 开始实例化")
        var obj = c.createObject(appHost, { "space": api })
        console.warn("[space] " + app.id + " 实例化返回, space 注入=" + (obj && obj.space ? "成功" : "失败"))
        if (!obj) {
            appError = space.st("create_failed", { err: c.errorString() })
            console.log("[space] 应用 " + app.id + " 创建失败: " + c.errorString())
            return
        }
        currentItem = obj
    }

    function closeApp(closing) {
        unmount()
        chrome = true
        appTitle = ""
        // 小组件可能要反映应用里的变化; 但整个启动台正在关闭时不刷 —— 回调会在外壳销毁后才回来
        if (!closing) reload()
    }

    function back() {
        if (!current) return
        if (api.onBack && api.onBack()) return
        closeApp()
    }

    function close() {
        closeApp(true)
        space.visible = false
        space.destroy()
    }

    // 外部程序型应用 (KOReader / 微信读书 / Android): 没有 QML 界面, 点图标即启动
    function launch(app) {
        var l = app.launch
        if (!l) { showToast(space.st("app_no_entry")); return }
        if (l.confirm && confirmApp !== app) { confirmApp = app; return }
        confirmApp = null
        if (l.type === "appload") {
            var helper = null
            try {
                helper = Qt.createQmlObject(
                    'import QtQuick;\nimport net.asivery.AppLoad;\n' +
                    'QtObject { function go(id){ AppLoadLauncher.launchApplication(id, [], ({}), false) } }',
                    space, "space_appload_launcher")
            } catch (e) { helper = null }
            if (helper) {
                space.visible = false
                helper.go(l.id)
                helper.destroy()
                close()
                return
            }
            if (!l.path) { showToast(space.st("appload_off")); return }
        }
        // post (或 appload 兜底): 交给 upload-server, 面板先收起来 (重启类操作后面没有界面了)。
        // 只隐藏不销毁: 请求还在路上, 销毁会把发请求的 JS 上下文一起收掉。
        var x = new XMLHttpRequest()
        x.open("POST", baseUrl + l.path)
        x.send()
        space.visible = false
    }

    // 挂载小组件: 与打开应用同一条路径 (createObject 时父项与初始属性都已就位)
    function mountWidget(host, app) {
        if (host.wItem) { host.wItem.destroy(); host.wItem = null }
        var compiled = app && app.module && app.module.widget
        if (!app || (!app.widgetUrl && !compiled)) return
        var c = compiled
                ? moduleComponent({ module: { uri: app.module.uri, main: app.module.widget } })
                : Qt.createComponent(bust(app.widgetUrl, app.rev))
        if (c.status === Component.Error) { console.log("[space] 小组件 " + app.id + ": " + c.errorString()); return }
        var o = c.createObject(host, { "space": api, "app": app })
        if (o) host.wItem = o
        else console.log("[space] 小组件 " + app.id + " 创建失败: " + c.errorString())
    }

    function showToast(msg) { toastText = msg; toastTimer.restart() }
    function tabTitle() {
        if (tab === "apps") return space.st("tab_apps")
        if (tab === "discover") return space.st("tab_store")
        if (tab === "settings") return space.st("tab_settings")
        return space.st("tab_home")
    }
    function dateLine() {
        return localNow().toLocaleDateString(Qt.locale(qtLocaleName()), space.st("date_format"))
    }

    Timer { id: toastTimer; interval: 4000; repeat: false; onTriggered: space.toastText = "" }
    Timer { interval: 15000; running: space.visible; repeat: true; onTriggered: space.now = new Date() }

    Component.onCompleted: { loadTheme(); reload(); loadShellCfg(); loadTimezones(); loadDevice(); hideApploadEntry(); libStart() }
    onVisibleChanged: { if (visible) { hideApploadEntry(); libStart() } else libStop() }
    Component.onDestruction: libStop()

    // ─── 文件库桥 (热导入) ──────────────────────────────────────────
    // 扫码上传的文档由 space-server (internal/xolib) 写进 xochitl 数据目录, 但要让文件库
    // 「看见」它, 得调 xochitl 内存里 Library 对象的 requestLoadEntry —— 外部进程够不着,
    // 只能由跑在 xochitl 里的外壳来调。所以外壳开着时长连接取活, 调完回报。
    // 外壳一隐藏/关闭就断开并说一声, 服务端随即把扫码上传关掉 (空间关了, 上传也关)。
    //
    // 不用 librarian: 它在 3.28 找不到改名的 Library 单例, 无限重试漏堆。Library 也不是
    // QML 单例 (527 个模块名都试过), 只能从界面树上拿 —— 文件库界面里 CalendarActions 的 .library。
    //
    // 3.29 起这里拿到的是只读的 LibraryFacade, 没有 requestLoadEntry。这时改用系统导入器
    // (xofm.libs.library 的 DocumentImporter 单例, USB 网页传文件用的那个) 的 importFromUrls,
    // 并在取活时报 mode=import, 服务端就把文档放进暂存目录交给导入器。3.28 及以前不碰导入器。
    property var xoLibrary: null
    property var xoImporterHolder: null
    property var xoCtrlHolder: null
    property string libMode: ""
    property var libXhr: null
    property bool libOn: false
    property int libFails: 0

    function findXoLibrary() {
        if (xoLibrary) return xoLibrary
        var top = space
        while (top.parent) top = top.parent
        var stack = [top], seen = 0
        while (stack.length && seen < 30000) {
            var n = stack.pop(); seen++
            if (String(n).indexOf("CalendarActions_") === 0 && n.library) { xoLibrary = n.library; return xoLibrary }
            var ch = n.children
            if (ch) for (var k = 0; k < ch.length; ++k) stack.push(ch[k])
        }
        return null
    }
    function xoImporter() {
        if (!xoImporterHolder) {
            try {
                xoImporterHolder = Qt.createQmlObject('import QtQml; import xofm.libs.library; QtObject { property var v: DocumentImporter }', space, "xoImporter")
            } catch (e) {
                console.warn("[space] 文件库: 没有导入器 " + e)
                return null
            }
        }
        var v = xoImporterHolder.v
        return (v && typeof v.importFromUrls === "function") ? v : null
    }
    // 3.29 起建文件夹 / 笔记本也交给系统: LibraryController.createCollection(上级, 名字) /
    // createDocument(上级, 名字), 返回新条目的 entry::Id (String() 即 uuid)
    function xoController() {
        if (!xoCtrlHolder) {
            try {
                xoCtrlHolder = Qt.createQmlObject('import QtQml; import xofm.libs.library; QtObject { property var v: LibraryController }', space, "xoCtrl")
            } catch (e) {
                return null
            }
        }
        return xoCtrlHolder.v || null
    }
    // 文件库对象找到之后才定模式: 有 requestLoadEntry 就是 load (3.28 及以前), 否则有导入器就是 import
    function detectLibMode() {
        var lib = findXoLibrary()
        if (!lib) return ""
        if (typeof lib.requestLoadEntry === "function") return "load"
        return xoImporter() ? "import" : "load"
    }
    function localFileUrl(path) {
        return "file://" + path.split("/").map(encodeURIComponent).join("/")
    }
    function libStart() {
        if (libOn) return
        libOn = true
        libFails = 0
        libPoll()
    }
    function libStop() {
        if (!libOn) return
        libOn = false
        libRetry.stop()
        if (libXhr) { try { libXhr.abort() } catch (e) {} libXhr = null }
        var x = new XMLHttpRequest()
        x.open("POST", baseUrl + "/space/library/bye")
        x.send()
    }
    // 长连接: 有活立刻返回, 没活等 25 秒回 204 再连。后台没起来时按 1/2/4…秒退避, 最多 60 秒。
    function libPoll() {
        if (!libOn) return
        var x = new XMLHttpRequest()
        libXhr = x
        x.onreadystatechange = function () {
            if (x.readyState !== 4 || !space || !space.libOn || space.libXhr !== x) return
            space.libXhr = null
            if (x.status === 200) {
                space.libFails = 0
                var r = null
                try { r = JSON.parse(x.responseText) } catch (e) { r = null }
                var jobs = (r && r.jobs) ? r.jobs : []
                for (var i = 0; i < jobs.length; ++i) space.libHandle(jobs[i])
                space.libPoll()
            } else if (x.status === 204) {
                space.libFails = 0
                space.libPoll()
            } else {
                space.libFails++
                libRetry.interval = Math.min(60000, 1000 * Math.pow(2, Math.min(space.libFails, 6)))
                libRetry.start()
            }
        }
        if (!libMode) libMode = detectLibMode()
        x.open("GET", baseUrl + "/space/library/next?wait=25" + (libMode ? "&mode=" + libMode : ""))
        x.send()
    }
    function libHandle(job) {
        var out = { op: job.op, id: job.id, ok: false }
        if (job.op === "import") {
            var imp = xoImporter()
            if (!imp) out.error = "找不到导入器 (DocumentImporter)"
            else try { imp.importFromUrls([localFileUrl(job.path)], job.parent || ""); out.ok = true } catch (e) { out.error = String(e) }
        } else if (job.op === "folder" || job.op === "notebook") {
            var ctrl = xoController()
            if (!ctrl) out.error = "找不到 LibraryController"
            else try {
                var nid = job.op === "folder" ? ctrl.createCollection(job.parent || "", job.name)
                                              : ctrl.createDocument(job.parent || "", job.name)
                out.newId = nid ? String(nid) : ""
                out.ok = out.newId !== ""
                if (!out.ok) out.error = "没有返回新条目"
            } catch (e) { out.error = String(e) }
        } else if (job.op === "load") {
            var lib = findXoLibrary()
            if (!lib) out.error = "找不到文件库对象 (CalendarActions.library)"
            else try { lib.requestLoadEntry(job.id); out.ok = true } catch (e) { out.error = String(e) }
        } else out.error = "不认识的操作: " + job.op
        console.warn("[space] 文件库 " + job.op + " " + job.id + " → " + (out.ok ? "ok" : out.error))
        request("POST", baseUrl + "/space/library/done", out, null)
    }
    Timer { id: libRetry; repeat: false; onTriggered: space.libPoll() }
    onTabChanged: {
        if (tab === "discover" && !storeLoaded) loadStore(false)
        if (tab === "discover" || tab === "settings") loadUpdate(false)
    }

    // ─── 空间自更新 (商店 → 系统 置顶一行; 设置 → 关于空间 只显示版本号) ───────
    // 后台 GET /space/update 给 当前版本 / 商店里的新版本 / 进行中的阶段; POST 开始。
    // 更新到最后后台会重启, 中间几秒请求失败是正常的, 轮询接着等。
    property var upd: null
    property bool updPolling: false
    function loadUpdate(refresh) {
        request("GET", baseUrl + "/space/update" + (refresh ? "?refresh=1" : ""), null, function (st, r) {
            if (st !== 200 || !r) return
            var was = space.upd
            space.upd = r
            if (r.rolledBack) space.showToast(space.st("upd_rolled_back"))
            var ph = r.state ? r.state.phase : ""
            if (ph === "error" && was && was.state && was.state.phase !== "error") space.showToast(space.st("upd_failed", { err: r.state.error || "" }))
            space.updPolling = ph !== "" && ph !== "error"
        })
    }
    function updCanStart() {
        return !!(upd && upd.available && (!upd.state || upd.state.phase === "" || upd.state.phase === "error"))
    }
    // \u8fdb\u884c\u4e2d\u7684\u9636\u6bb5\u6587\u5b57; \u7a7a\u95f2\u8fd4\u56de ""
    function updPhaseText() {
        var stt = (upd && upd.state) ? upd.state : {}
        if (stt.phase === "downloading") return space.st("upd_downloading", { pct: stt.total > 0 ? Math.floor(stt.got * 100 / stt.total) : 0 })
        if (stt.phase === "verifying" || stt.phase === "testing" || stt.phase === "switching") return space.st("upd_installing")
        if (stt.phase === "restarting") return space.st("upd_restarting")
        return ""
    }
    // \u5173\u4e8e\u7a7a\u95f4: \u6c38\u8fdc\u5148\u7ed9\u5f53\u524d\u7248\u672c\u53f7; \u6709\u65b0\u7248\u672c\u65f6\u63d0\u793a\u53bb\u5546\u5e97, \u5426\u5219\u8ddf\u7248\u6743
    function updDetail() {
        var cur = (upd && upd.current) ? upd.current : ""
        if (upd && upd.available) return cur + "\u3000" + space.st("about_new", { ver: upd.latest })
        return cur + "\u3000Copyright \u00a9 2026 Boangs"
    }
    // \u5546\u5e97\u300c\u7cfb\u7edf\u8865\u4e01\u300d\u680f\u7a7a\u95f4\u90a3\u4e00\u884c\u7684\u526f\u6587
    function updRowDetail() {
        var cur = (upd && upd.current) ? upd.current : ""
        var ph = updPhaseText()
        if (ph) return ph
        if (upd && upd.available) return space.st("upd_row_available", { cur: cur, ver: upd.latest })
        return space.st("upd_row_latest", { cur: cur })
    }
    function gotoSystemUpdates() { storeSection = "system"; tab = "discover" }
    function askUpdate() {
        if (!updCanStart()) return
        var notes = (space.lang === "zh-CN" ? upd.notes : (upd.notes_en || upd.notes)) || ""
        var mb = upd.size ? Math.max(1, Math.round(upd.size / 1048576)) : 0
        ask(space.st("upd_confirm", { ver: upd.latest, mb: mb }) + (notes ? "\n\n" + notes : ""), function () {
            request("POST", baseUrl + "/space/update", {}, function (st, r) {
                if (st !== 200) { space.showToast(space.st("upd_failed", { err: (r && r.error) ? r.error : st })); return }
                space.updPolling = true
                space.loadUpdate(false)
            })
        })
    }
    Timer {
        interval: 2000; repeat: true
        running: space.updPolling && space.visible
        onTriggered: space.loadUpdate(false)
    }

    // ─── 应用内导航栏 (返回 + 标题) ───────────────────────────────────
    Item {
        id: appNav
        visible: space.chrome && space.current !== null
        anchors.top: parent.top
        anchors.left: parent.left
        anchors.right: parent.right
        height: space.vpx(72)
        IconButton {
            anchors.top: parent.top
            anchors.topMargin: 10
            anchors.left: parent.left
            anchors.leftMargin: 10
            iconSource: "qrc:/ark/icons/chevron_left"
            title: space.st("back")
            onClicked: space.back()
        }
    }
    Text {
        id: appTitleText
        visible: space.chrome && space.current !== null
        anchors.top: appNav.bottom
        anchors.topMargin: space.vpx(56)
        anchors.left: parent.left
        anchors.leftMargin: space.margin
        text: space.current ? (space.appTitle || space.current.name) : ""
        font.pixelSize: space.fpx(56)
        font.weight: Font.Medium
    }

    // ─── 标签页页眉 (大标题 + 副标题 + 齿轮) ──────────────────────────
    Item {
        id: pageHeader
        visible: space.chrome && space.current === null
        anchors.top: parent.top
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.leftMargin: space.margin
        anchors.rightMargin: space.margin
        // 副标题已去掉 (2026-10-03)。高度 = 标题底 + 24:
        // 标题 y 42 + 标题高 ~78 = 120, 再留 24。省下的 72 给首页大卡。
        height: space.vpx(144)
        // 返回只留一个 "<" 并入标题行: 原来单占一行加space.st("back")二字, 白吃 70px,
        // 又与整页的克制气质不搭。退出空间在设置里也有一条, 这里是一步直达。
        // < 不是主角: 挪到内容边距之外、贴近屏幕左沿, 把内容左边界让给标题,
        // 让space.st("tab_home")与下面所有卡片的左边对齐。
        Image {
            id: backChevron
            anchors.left: parent.left
            anchors.leftMargin: space.px(16) - space.margin
            anchors.verticalCenter: pageTitle.verticalCenter
            width: space.fpx(34); height: width
            sourceSize.width: width; sourceSize.height: width
            source: space.icon("caret-right")
            rotation: 180
            fillMode: Image.PreserveAspectFit
            MouseArea { anchors.fill: parent; anchors.margins: -20; onClicked: space.close() }
        }
        Text {
            id: pageTitle
            x: 0
            y: space.vpx(42)
            text: space.tabTitle()
            font.pixelSize: space.fpx(56)
            font.weight: Font.Medium
        }
        Image {
            visible: space.tab !== "home"
            anchors.right: parent.right
            anchors.verticalCenter: pageTitle.verticalCenter
            width: space.fpx(44); height: width
            source: "qrc:/ark/icons/restore"
            fillMode: Image.PreserveAspectFit
            MouseArea {
                anchors.fill: parent; anchors.margins: -16
                onClicked: { space.reload(); if (space.tab === "discover") space.loadStore(true) }
            }
        }
    }

    // 提示条: 4 秒自动消失, 盖在内容区顶部, 不挤动布局
    Rectangle {
        visible: space.toastText !== ""
        z: 5
        anchors.top: content.top
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.leftMargin: space.margin
        anchors.rightMargin: space.margin
        height: toastLabel.implicitHeight + 24
        color: space.th.primary
        radius: 8
        Text {
            id: toastLabel
            anchors.fill: parent
            anchors.margins: 12
            text: space.toastText
            font.pixelSize: space.fpx(24)
            color: space.th.primaryText
            wrapMode: Text.WordWrap
        }
    }

    // ─── 内容区 ───────────────────────────────────────────────────────
    Item {
        id: content
        anchors.top: !space.chrome ? parent.top : (space.current ? appTitleText.bottom : pageHeader.bottom)
        anchors.topMargin: !space.chrome ? 0 : (space.current ? space.vpx(48) : 0)
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.bottom: (space.chrome && space.current === null) ? tabBar.top : parent.bottom
        anchors.leftMargin: space.chrome ? space.margin : 0
        anchors.rightMargin: space.chrome ? space.margin : 0
        anchors.bottomMargin: space.chrome ? space.vpx(24) : 0

        // ── 空间 (首页) ──
        Flickable {
            visible: space.current === null && space.tab === "home"
            anchors.fill: parent
            contentWidth: width
            contentHeight: homeCol.height + space.vpx(24)   // 底部留白与块间距一致
            clip: true
            ColumnLayout {
                id: homeCol
                width: parent.width
                spacing: space.vpx(24)

                // 主题替换的「hero」区域 (themes/README.md): 根 Item 带 property var space, 自己给 implicitHeight
                Loader {
                    Layout.fillWidth: true
                    active: !!space.th.slots.hero
                    visible: active
                    source: space.th.slots.hero || ""
                    Layout.preferredHeight: item ? item.implicitHeight : 0
                    onLoaded: item.space = space
                }
                // 顶部大卡: 日期 + 时间; 装了 hero 小组件 (如天气) 则由它整卡接管
                Rectangle {
                    visible: !space.th.slots.hero
                    Layout.fillWidth: true
                    Layout.preferredHeight: space.vpx(562)   // 副标题省下的高度匀给插图
                    radius: space.th.radius
                    border.color: space.th.line
                    border.width: space.th.border
                    color: space.th.card
                    clip: true
                    Item {
                        anchors.fill: parent
                        visible: !space.heroWidgetEnabled() || space.heroWidgetApp() === null
                        // 没装 hero 小组件 (比如天气被删掉了) 时的默认样子: 一幅山水垫底,
                        // 日期时间压在上面。素材是 2:1 的横幅, 贴着底边按原比例铺开 ——
                        // 上面留出的白正好垫文字, 两者不重叠, 所以不需要蒙版或渐隐。
                        Image {
                            id: heroArt
                            anchors.bottom: parent.bottom
                            anchors.left: parent.left; anchors.right: parent.right
                            height: Math.min(parent.height, Math.round(parent.width / 2))
                            source: space.heroImageUrl()
                            fillMode: Image.PreserveAspectCrop
                            // 解码到实际显示尺寸, 别按原图 1200 宽解了再缩 —— 墨水屏上白费内存
                            sourceSize.width: width
                            visible: status === Image.Ready
                        }
                        // 横幅是方角、卡片是圆角, 底下两角会戳出来。设备上没有
                        // QtQuick.Effects 做遮罩 (Qt5Compat.GraphicalEffects 也没有),
                        // clip 又只做矩形裁剪、不跟圆角走 —— 照搬天气小组件的办法, 用 Canvas
                        // 在两角各补一小块"方角减去那道弧"的白, 四边不留白, 图与描边贴合。
                        Component {
                            id: heroCornerCut
                            Canvas {
                                property bool atRight: false
                                property color fill: space.th.card
                                onFillChanged: requestPaint()
                                width: 14; height: 14
                                onPaint: {
                                    var ctx = getContext("2d")
                                    ctx.reset()
                                    ctx.fillStyle = fill
                                    ctx.beginPath()
                                    if (atRight) {
                                        ctx.moveTo(width, 0); ctx.lineTo(width, height); ctx.lineTo(0, height)
                                        ctx.arc(0, 0, width, Math.PI / 2, 0, true)
                                    } else {
                                        ctx.moveTo(0, 0); ctx.lineTo(0, height); ctx.lineTo(width, height)
                                        ctx.arc(width, 0, width, Math.PI / 2, Math.PI, false)
                                    }
                                    ctx.closePath(); ctx.fill()
                                }
                            }
                        }
                        Loader {
                            sourceComponent: heroCornerCut
                            anchors.left: parent.left; anchors.bottom: parent.bottom
                        }
                        Loader {
                            sourceComponent: heroCornerCut
                            anchors.right: parent.right; anchors.bottom: parent.bottom
                            onLoaded: { item.atRight = true; item.requestPaint() }
                        }
                        // 卡片自己的细描边被画盖住了, 补回来 —— 否则上半截有框下半截没有
                        Rectangle {
                            anchors.fill: parent
                            color: "transparent"; radius: space.th.radius
                            border.color: space.th.line; border.width: space.th.border
                        }
                        Column {
                            anchors.left: parent.left
                            anchors.leftMargin: space.px(32)
                            anchors.top: parent.top
                            anchors.topMargin: space.vpx(40)
                            spacing: 8
                            Text { text: space.dateLine(); font.pixelSize: space.fpx(26); color: space.th.text2 }
                            Text { text: Qt.formatTime(space.localNow(), "HH:mm"); font.pixelSize: space.fpx(88); font.weight: Font.Light }
                            Text { text: space.st("hero_quiet"); font.pixelSize: space.fpx(22); color: space.th.text3 }
                        }
                    }
                    Item {
                        id: heroHost
                        anchors.fill: parent
                        property var wItem: null
                        property var heroApp: space.heroWidgetEnabled() ? space.heroWidgetApp() : null
                        onHeroAppChanged: space.mountWidget(heroHost, heroApp)
                        Component.onCompleted: space.mountWidget(heroHost, heroApp)
                    }
                }

                // ── 今日计划 / 正在进行: 左右两张卡 (照概念图) ────────────────
                // 左卡交给声明了 widget_slot: "plan" 的应用; 没装就显示引导。
                // 右卡是外壳自带: 最近在读 (/space/recent, 读 xochitl 元数据) + 其余半宽小组件。
                // 主题替换的「widgets」区域 (themes/README.md): 根 Item 带 property var space, 自己给 implicitHeight
                Loader {
                    Layout.fillWidth: true
                    active: !!space.th.slots.widgets
                    visible: active
                    source: space.th.slots.widgets || ""
                    Layout.preferredHeight: item ? item.implicitHeight : 0
                    onLoaded: item.space = space
                }
                GridLayout {
                    visible: !space.th.slots.widgets
                    Layout.fillWidth: true
                    columns: 2
                    columnSpacing: space.px(24)
                    rowSpacing: space.vpx(24)

                    Rectangle {
                        Layout.fillWidth: true
                        Layout.preferredHeight: space.vpx(350)
                        radius: space.th.radius; border.color: space.th.line; border.width: space.th.border; color: space.th.card; clip: true
                        readonly property var planApp: space.slotWidgetApp("plan")

                        Column {
                            anchors.fill: parent; anchors.margins: space.px(22); spacing: space.vpx(12)
                            visible: parent.planApp === null
                            Text { text: space.st("plan_today"); font.pixelSize: space.fpx(28); font.weight: Font.Medium; color: space.th.text }
                            Text {
                                width: parent.width
                                text: space.st("plan_empty")
                                font.pixelSize: space.fpx(22); color: space.th.textNote; wrapMode: Text.WordWrap
                            }
                        }
                        Item {
                            id: planHost
                            anchors.fill: parent
                            property var wItem: null
                            visible: parent.planApp !== null
                            onVisibleChanged: if (visible) space.mountWidget(planHost, parent.planApp)
                            Component.onCompleted: if (parent.planApp) space.mountWidget(planHost, parent.planApp)
                        }
                        MouseArea {
                            anchors.fill: parent
                            visible: parent.planApp === null
                            onClicked: space.tab = "discover"
                        }
                    }

                    Rectangle {
                        Layout.fillWidth: true
                        Layout.preferredHeight: space.vpx(350)
                        radius: space.th.radius; border.color: space.th.line; border.width: space.th.border; color: space.th.card; clip: true
                        readonly property bool hasRecent: space.recentDoc !== null
                        readonly property bool hasHalf: space.plainWidgetApps().length > 0

                        Column {
                            anchors.fill: parent; anchors.margins: space.px(22); spacing: space.vpx(10)
                            Text { text: space.st("ongoing"); font.pixelSize: space.fpx(28); font.weight: Font.Medium; color: space.th.text }

                            Item {
                                width: parent.width
                                height: space.vpx(92)
                                visible: parent.parent.hasRecent
                                Image {
                                    id: bookIcon
                                    anchors.left: parent.left; anchors.verticalCenter: parent.verticalCenter
                                    width: space.fpx(44); height: width
                                    sourceSize.width: width; sourceSize.height: width
                                    source: space.icon("book-open")
                                    fillMode: Image.PreserveAspectFit
                                }
                                Column {
                                    anchors.left: bookIcon.right; anchors.leftMargin: space.px(16)
                                    anchors.right: parent.right
                                    anchors.verticalCenter: parent.verticalCenter
                                    spacing: space.vpx(8)
                                    Text {
                                        width: parent.width
                                        text: space.recentDoc ? space.recentDoc.name : ""
                                        font.pixelSize: space.fpx(25); color: space.th.text; elide: Text.ElideRight
                                    }
                                    Text {
                                        text: space.recentDoc && space.recentDoc.pages > 0
                                              ? space.st("reading_progress", { pct: space.recentDoc.percent })
                                              : (space.recentDoc ? space.st("reading_page", { page: space.recentDoc.page }) : "")
                                        font.pixelSize: space.fpx(21); color: space.th.textNote
                                    }
                                    Rectangle {
                                        width: parent.width; height: 4; radius: 2; color: space.th.lineSoft
                                        visible: !!space.recentDoc && space.recentDoc.pages > 0
                                        Rectangle {
                                            anchors.left: parent.left; anchors.top: parent.top; anchors.bottom: parent.bottom
                                            width: Math.max(4, parent.width * (space.recentDoc ? space.recentDoc.percent : 0) / 100)
                                            radius: 2; color: space.th.text
                                        }
                                    }
                                }
                                MouseArea { anchors.fill: parent; onClicked: space.exit() }
                            }

                            Rectangle {
                                width: parent.width; height: 1; color: space.th.lineSoft
                                visible: parent.parent.hasRecent && parent.parent.hasHalf
                            }

                            Repeater {
                                model: space.plainWidgetApps()
                                delegate: Item {
                                    required property var modelData
                                    width: parent.width
                                    height: space.vpx(92)
                                    property var wItem: null
                                    Component.onCompleted: space.mountWidget(this, modelData)
                                }
                            }
                        }
                    }
                }

                RowLayout {
                    visible: !space.th.slots.apps
                    Layout.fillWidth: true
                    Text { text: space.st("my_apps"); font.pixelSize: space.fpx(32); font.weight: Font.Medium }
                    Item { Layout.fillWidth: true }
                    Text {
                        text: space.st("all_apps")
                        font.pixelSize: space.fpx(24)
                        color: space.th.text2
                        MouseArea { anchors.fill: parent; anchors.margins: -12; onClicked: space.tab = "apps" }
                    }
                }
                Text {
                    visible: space.listError !== ""
                    text: space.listError
                    font.pixelSize: space.fpx(24)
                    color: "#a00000"
                    wrapMode: Text.WordWrap
                    Layout.fillWidth: true
                }
                Text {
                    visible: space.listError === "" && space.apps.length === 0
                    text: space.st("apps_empty")
                    font.pixelSize: space.fpx(24)
                    color: space.th.text2
                    wrapMode: Text.WordWrap
                    Layout.fillWidth: true
                }
                GridLayout {
                    id: homeGrid
                    visible: !space.th.slots.apps
                    Layout.fillWidth: true
                    columns: space.cols(width, 200)
                    rowSpacing: space.vpx(24)
                    columnSpacing: space.px(24)
                    Repeater {
                        model: space.homeApps()
                        delegate: Kit.STile {
                            th: space.th
                            required property var modelData
                            Layout.fillWidth: true
                            Layout.preferredHeight: space.vpx(170)
                            iconSize: space.fpx(62)
                            labelSize: space.fpx(26)
                            iconSource: space.bust(modelData.iconUrl || "", modelData.rev)
                            label: modelData.name
                            onClicked: space.open(modelData)
                        }
                    }
                }
                // 主题替换的「apps」区域 (themes/README.md): 根 Item 带 property var space, 自己给 implicitHeight
                Loader {
                    Layout.fillWidth: true
                    active: !!space.th.slots.apps
                    visible: active
                    source: space.th.slots.apps || ""
                    Layout.preferredHeight: item ? item.implicitHeight : 0
                    onLoaded: item.space = space
                }
            }
        }

        // ── 应用 (全部) ──
        Flickable {
            visible: space.current === null && space.tab === "apps"
            anchors.fill: parent
            contentWidth: width
            contentHeight: appsGrid.height + space.vpx(24)
            clip: true
            GridLayout {
                id: appsGrid
                width: parent.width
                columns: space.cols(width, 270)
                rowSpacing: space.vpx(24)
                columnSpacing: space.px(24)
                Repeater {
                    model: space.userApps()
                    delegate: Kit.STile {
                        th: space.th
                        required property var modelData
                        Layout.fillWidth: true
                        Layout.preferredHeight: space.vpx(220)
                        iconSize: space.fpx(76)
                        labelSize: space.fpx(28)
                        iconSource: space.bust(modelData.iconUrl || "", modelData.rev)
                        label: modelData.name
                        dimmed: !!modelData.error
                        onClicked: space.open(modelData)
                    }
                }
            }
        }

        // ── 发现 (应用商店) ──
        Flickable {
            visible: space.current === null && space.tab === "discover"
            anchors.fill: parent
            contentWidth: width
            contentHeight: storeCol.height + space.vpx(24)
            clip: true
            ColumnLayout {
                id: storeCol
                width: parent.width
                spacing: 0
                Rectangle {
                    Layout.fillWidth: true
                    Layout.preferredHeight: space.vpx(150)
                    Layout.bottomMargin: space.vpx(24)
                    radius: space.th.radius
                    color: space.th.fill
                    Column {
                        anchors.centerIn: parent
                        spacing: 8
                        Text { anchors.horizontalCenter: parent.horizontalCenter; text: space.st("hero_tagline_1"); font.pixelSize: space.fpx(34); font.weight: Font.Medium }
                        Text { anchors.horizontalCenter: parent.horizontalCenter; text: space.st("hero_tagline_2"); font.pixelSize: space.fpx(24); color: space.th.textBody }
                    }
                }
                // 分栏: 应用 / 主题 / 系统
                Item {
                    Layout.fillWidth: true
                    Layout.preferredHeight: space.vpx(76)
                    Layout.bottomMargin: space.vpx(8)
                    Row {
                        anchors.fill: parent
                        Repeater {
                            model: space.storeSections
                            delegate: Item {
                                id: secItem
                                required property string modelData
                                readonly property bool active: space.storeSection === modelData
                                width: parent.width / space.storeSections.length
                                height: parent.height
                                Text {
                                    anchors.centerIn: parent
                                    text: space.st("store_sec_" + secItem.modelData)
                                    font.pixelSize: space.fpx(26)
                                    font.weight: secItem.active ? Font.Medium : Font.Normal
                                    color: secItem.active ? space.th.text : space.th.text3
                                }
                                Rectangle {
                                    anchors.bottom: parent.bottom; anchors.horizontalCenter: parent.horizontalCenter
                                    width: parent.width - space.px(24); height: 3
                                    color: secItem.active ? space.th.text : "transparent"
                                }
                                MouseArea { anchors.fill: parent; onClicked: space.storeSection = secItem.modelData }
                            }
                        }
                    }
                    Rectangle { anchors.left: parent.left; anchors.right: parent.right; anchors.bottom: parent.bottom; height: 1; color: space.th.lineSoft }
                }
                // 系统栏置顶: 空间本体 (数据来自 /space/update, 不在商店列表里)
                Item {
                    visible: space.storeSection === "system" && !!space.upd
                    Layout.fillWidth: true
                    Layout.preferredHeight: space.vpx(120)
                    RowLayout {
                        anchors.fill: parent
                        spacing: space.px(20)
                        Rectangle {
                            Layout.preferredWidth: space.fpx(72); Layout.preferredHeight: space.fpx(72); radius: space.th.radius
                            color: space.th.fill
                            Image {
                                anchors.centerIn: parent
                                width: space.fpx(40); height: width
                                source: space.icon("squares-four")
                                sourceSize.width: width; sourceSize.height: width
                            }
                        }
                        ColumnLayout {
                            Layout.fillWidth: true
                            spacing: 4
                            Text { text: space.st("tab_home"); font.pixelSize: space.fpx(28); elide: Text.ElideRight; Layout.fillWidth: true }
                            Text { text: space.updRowDetail(); font.pixelSize: space.fpx(22); color: space.th.text3; elide: Text.ElideRight; Layout.fillWidth: true }
                        }
                        Kit.SButton {
                            th: space.th
                            ui: space.fs
                            readonly property bool busy: space.updPhaseText() !== ""
                            enabled: space.updCanStart()
                            primary: !!(space.upd && space.upd.available)
                            text: busy ? space.st("act_updating")
                                  : ((space.upd && space.upd.available) ? space.st("act_update") : space.st("act_installed"))
                            onClicked: space.askUpdate()
                        }
                    }
                    Rectangle { anchors.left: parent.left; anchors.right: parent.right; anchors.bottom: parent.bottom; height: 1; color: space.th.lineSoft }
                }
                Text {
                    visible: !space.storeLoaded
                    text: space.st("store_loading")
                    font.pixelSize: space.fpx(24); color: space.th.textBody
                }
                Text {
                    visible: space.storeError !== ""
                    Layout.fillWidth: true
                    text: space.storeError + "\n" + space.st("store_offline")
                    font.pixelSize: space.fpx(24); color: space.th.textBody; wrapMode: Text.WordWrap
                }
                Text {
                    visible: space.storeLoaded && space.storeError === "" && space.storeList().length === 0
                    Layout.topMargin: space.vpx(16)
                    text: space.st("store_empty_" + space.storeSection)
                    font.pixelSize: space.fpx(24); color: space.th.textBody
                }
                Repeater {
                    model: space.storeList()
                    delegate: Item {
                        id: storeRow
                        required property var modelData
                        readonly property string installed: space.installedVersion(modelData.id)
                        readonly property var openable: space.storeSection === "system" ? space.installedWithUi(modelData.id) : null
                        Layout.fillWidth: true
                        Layout.preferredHeight: space.vpx(120)
                        Rectangle { anchors.fill: parent; color: rowMouse.pressed ? space.th.press : "transparent" }
                        MouseArea {
                            id: rowMouse
                            anchors.fill: parent
                            enabled: storeRow.openable !== null
                            onClicked: space.open(storeRow.openable)
                        }
                        RowLayout {
                            anchors.fill: parent
                            spacing: space.px(20)
                            Rectangle {
                                Layout.preferredWidth: space.fpx(72); Layout.preferredHeight: space.fpx(72); radius: space.th.radius
                                color: space.th.fill
                                Text { anchors.centerIn: parent; text: (storeRow.modelData.name || "?").charAt(0); font.pixelSize: space.fpx(32) }
                            }
                            ColumnLayout {
                                Layout.fillWidth: true
                                spacing: 4
                                Text { text: storeRow.modelData.name || storeRow.modelData.id; font.pixelSize: space.fpx(28); elide: Text.ElideRight; Layout.fillWidth: true }
                                Text {
                                    text: (storeRow.modelData.description || "") + "  v" + (storeRow.modelData.version || "")
                                          + (storeRow.modelData.locked === true
                                             ? "\u3000" + space.st("need_purchase") + (storeRow.modelData.price ? " " + storeRow.modelData.price : "")
                                             : "")
                                    font.pixelSize: space.fpx(22); color: space.th.text3; elide: Text.ElideRight; Layout.fillWidth: true
                                }
                            }
                            // 装过的才给删除按钮; 内置应用不在商店里, 不用担心误删
                            Kit.SButton {
                                th: space.th
                                ui: space.fs
                                visible: storeRow.installed !== ""
                                text: space.st("act_delete")
                                onClicked: space.ask(
                                    space.st("confirm_delete", { name: storeRow.modelData.name || storeRow.modelData.id }) + "\n"
                                    + space.st("confirm_delete_note"),
                                    function () { space.uninstallApp(storeRow.modelData) })
                            }
                            Kit.SButton {
                                th: space.th
                                ui: space.fs
                                readonly property bool locked: storeRow.modelData.locked === true
                                // 线上比本机新才算有更新 (本机更新或一样 = 已安装, 不让点, 免得降级)
                                readonly property bool newer: storeRow.installed !== ""
                                    && space.versionCmp(storeRow.modelData.version, storeRow.installed) > 0
                                enabled: locked || storeRow.installed === "" || newer
                                text: locked ? space.st("act_buy")
                                      : (storeRow.installed === "" ? space.st("act_get")
                                         : (newer ? space.st("act_update") : space.st("act_installed")))
                                primary: locked || storeRow.installed === ""
                                onClicked: {
                                    if (locked) space.buyPrompt(storeRow.modelData)
                                    else space.installFromStore(storeRow.modelData)
                                }
                            }
                        }
                        Rectangle { anchors.left: parent.left; anchors.right: parent.right; anchors.bottom: parent.bottom; height: 1; color: space.th.lineSoft }
                    }
                }
            }
        }

        // ── 设置 (设置类应用的行列表) ──
        Flickable {
            visible: space.current === null && space.tab === "settings"
            anchors.fill: parent
            contentWidth: width
            contentHeight: mineCol.height + space.vpx(24)
            clip: true
            ColumnLayout {
                id: mineCol
                width: parent.width
                spacing: 0
                Repeater {
                    model: space.settingsApps()
                    delegate: Kit.SRow {
                        th: space.th
                        required property var modelData
                        Layout.fillWidth: true
                        ui: space.fs
                        iconSource: space.bust(modelData.iconUrl || "", modelData.rev)
                        text: modelData.name
                        onClicked: space.open(modelData)
                    }
                }
                Kit.SRow {
                    th: space.th
                    // 能读到系统语言时空间直接跟系统走, 这一项就没意义了
                    visible: space.langSystem === ""
                    Layout.fillWidth: true
                    ui: space.fs
                    iconSource: space.icon("translate")
                    text: space.st("set_language")
                    detail: space.langNativeName(space.lang)
                    onClicked: space.chooseLang(space.nextLang(space.lang))
                }
                Kit.SRow {
                    th: space.th
                    Layout.fillWidth: true
                    ui: space.fs
                    iconSource: space.icon("sun")
                    text: space.st("set_screenmode")
                    detail: space.screenModeLabel(space.screenMode)
                    onClicked: space.screenModeOpen = !space.screenModeOpen
                }
                // 刷新档位: 就地展开, 当前的打勾; 选了立刻生效并收起
                Repeater {
                    model: space.screenModeOpen ? space.screenModes : []
                    delegate: Item {
                        required property string modelData
                        Layout.fillWidth: true
                        implicitHeight: space.fpx(96)
                        Rectangle { anchors.fill: parent; color: smMouse.pressed ? space.th.press : "transparent" }
                        Column {
                            anchors.left: parent.left; anchors.leftMargin: space.fpx(72)
                            anchors.right: smCheck.left; anchors.rightMargin: space.fpx(16)
                            anchors.verticalCenter: parent.verticalCenter
                            spacing: space.fpx(4)
                            Text { text: space.screenModeLabel(modelData); font.pixelSize: space.fpx(26) }
                            Text {
                                width: parent.width
                                text: space.st("screenmode_" + modelData + "_hint")
                                font.pixelSize: space.fpx(20); color: space.th.text3
                                elide: Text.ElideRight
                            }
                        }
                        Image {
                            id: smCheck
                            anchors.right: parent.right; anchors.rightMargin: space.fpx(24)
                            anchors.verticalCenter: parent.verticalCenter
                            width: space.fpx(28); height: width
                            sourceSize.width: width; sourceSize.height: height
                            source: space.icon("check")
                            opacity: modelData === space.screenMode ? 1 : 0
                        }
                        Rectangle { anchors.left: parent.left; anchors.leftMargin: space.fpx(72); anchors.right: parent.right; anchors.bottom: parent.bottom; height: 1; color: space.th.lineFaint }
                        MouseArea {
                            id: smMouse
                            anchors.fill: parent
                            onClicked: space.chooseScreenMode(modelData)
                        }
                    }
                }
                Kit.SRow {
                    th: space.th
                    Layout.fillWidth: true
                    ui: space.fs
                    iconSource: space.icon("clock")
                    text: space.st("set_timezone")
                    detail: space.tzLabel(space.tzName) + "\u3000" + space.tzOffsetText(space.tzOffset)
                    onClicked: {
                        if (!space.tzOpen && space.tzList.length === 0) space.loadTimezones()
                        space.tzOpen = !space.tzOpen
                    }
                }
                // 时区列表: 就地展开, 当前的打勾; 选完收起
                Text {
                    visible: space.tzOpen
                    Layout.fillWidth: true
                    Layout.leftMargin: space.fpx(72)
                    Layout.rightMargin: space.fpx(16)
                    Layout.topMargin: space.fpx(8)
                    text: space.st("tz_hint")
                    wrapMode: Text.WordWrap
                    font.pixelSize: space.fpx(20)
                    color: space.th.text3
                }
                Repeater {
                    model: space.tzOpen ? space.tzList : []
                    delegate: Item {
                        required property var modelData
                        Layout.fillWidth: true
                        implicitHeight: space.fpx(76)
                        Rectangle { anchors.fill: parent; color: tzMouse.pressed ? space.th.press : "transparent" }
                        Text {
                            anchors.left: parent.left; anchors.leftMargin: space.fpx(72)
                            anchors.right: tzOff.left; anchors.rightMargin: space.fpx(16)
                            anchors.verticalCenter: parent.verticalCenter
                            text: space.lang === "zh-CN" ? modelData.zh : modelData.en
                            font.pixelSize: space.fpx(26)
                            elide: Text.ElideRight
                        }
                        Text {
                            id: tzOff
                            anchors.right: tzCheck.left; anchors.rightMargin: space.fpx(16)
                            anchors.verticalCenter: parent.verticalCenter
                            text: space.tzOffsetText(modelData.offset)
                            font.pixelSize: space.fpx(22)
                            color: space.th.text3
                        }
                        Image {
                            id: tzCheck
                            anchors.right: parent.right; anchors.rightMargin: space.fpx(24)
                            anchors.verticalCenter: parent.verticalCenter
                            width: space.fpx(28); height: width
                            sourceSize.width: width; sourceSize.height: height
                            source: space.icon("check")
                            opacity: modelData.name === space.tzName ? 1 : 0
                        }
                        Rectangle { anchors.left: parent.left; anchors.leftMargin: space.fpx(72); anchors.right: parent.right; anchors.bottom: parent.bottom; height: 1; color: space.th.lineFaint }
                        MouseArea { id: tzMouse; anchors.fill: parent; onClicked: space.chooseTimezone(modelData.name) }
                    }
                }
                // 关于空间: 只显示版本号 (当前 + 商店里的最新); 更新本身在商店「系统」栏, 有新版本时点这行跳过去
                Kit.SRow {
                    th: space.th
                    Layout.fillWidth: true
                    ui: space.fs
                    iconSource: space.icon("squares-four")
                    text: space.st("about")
                    detail: space.updDetail()
                    interactive: !!(space.upd && space.upd.available)
                    onClicked: space.gotoSystemUpdates()
                }
                // 关于空间下面: 作者的小红书二维码, 居中, 文字在下面
                Column {
                    Layout.alignment: Qt.AlignHCenter
                    Layout.topMargin: space.fpx(48)
                    spacing: space.fpx(10)
                    Image {
                        anchors.horizontalCenter: parent.horizontalCenter
                        width: space.fpx(300); height: width
                        source: space.kitDir + "/images/xhs-qr.png?v=4"   // 换图要改这个号: xochitl 按地址缓存图片, 不改地址换了文件也不重读
                        sourceSize.width: 600; sourceSize.height: 600
                        fillMode: Image.PreserveAspectFit
                    }
                    Text { anchors.horizontalCenter: parent.horizontalCenter; text: space.st("about_xhs"); font.pixelSize: space.fpx(24); color: space.th.text }
                    Text { anchors.horizontalCenter: parent.horizontalCenter; text: space.st("about_xhs_id"); font.pixelSize: space.fpx(20); color: space.th.text }
                }
            }
        }

        // ── 应用宿主: 应用根 Item 会以它为父, 自己 anchors.fill: parent ──
        Item {
            id: appHost
            anchors.fill: parent
            visible: space.current !== null
        }
        Text {
            visible: space.appError !== ""
            anchors.fill: parent
            text: space.st("app_load_failed") + "\n" + space.appError
            font.pixelSize: space.fpx(24)
            color: "#a00000"
            wrapMode: Text.WrapAnywhere
        }
    }

    // ─── 底部标签栏 ───────────────────────────────────────────────────
    Rectangle {
        id: tabBar
        visible: space.chrome && space.current === null
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.bottom: parent.bottom
        height: space.vpx(120)
        color: space.th.bg
        Rectangle { anchors.top: parent.top; anchors.left: parent.left; anchors.right: parent.right; height: 1; color: space.th.line }
        RowLayout {
            anchors.fill: parent
            anchors.leftMargin: space.margin
            anchors.rightMargin: space.margin
            spacing: 0
            Repeater {
                model: [
                    { key: "home", label: space.st("tab_home"), icon: "house" },
                    { key: "apps", label: space.st("app_fallback_name"), icon: "squares-four" },
                    { key: "discover", label: space.st("tab_store"), icon: "compass" },
                    { key: "settings", label: space.st("tab_settings"), icon: "gear" }
                ]
                delegate: Item {
                    id: tabItem
                    required property var modelData
                    readonly property bool active: space.tab === modelData.key
                    Layout.fillWidth: true
                    Layout.fillHeight: true
                    Column {
                        anchors.centerIn: parent
                        spacing: 6
                        Image {
                            anchors.horizontalCenter: parent.horizontalCenter
                            width: space.fpx(40); height: width
                            source: space.kitDir + "/icons/" + tabItem.modelData.icon + ".svg"
                            fillMode: Image.PreserveAspectFit
                            sourceSize.width: width; sourceSize.height: width
                            opacity: tabItem.active ? 1 : 0.55
                        }
                        Text {
                            anchors.horizontalCenter: parent.horizontalCenter
                            text: tabItem.modelData.label
                            font.pixelSize: space.fpx(22)
                            font.weight: tabItem.active ? Font.Medium : Font.Normal
                            color: tabItem.active ? space.th.text : space.th.text3
                        }
                        Rectangle {
                            anchors.horizontalCenter: parent.horizontalCenter
                            width: 28; height: 3
                            color: tabItem.active ? space.th.text : "transparent"
                        }
                    }
                    MouseArea {
                        anchors.fill: parent
                        onClicked: space.tab = tabItem.modelData.key
                    }
                }
            }
        }
    }

    // ─── 遮罩: 后台启动中 / 安装中 ─────────────────────────────────────
    Rectangle {
        visible: space.busyText !== ""
        anchors.fill: parent
        z: 10
        color: "#ccffffff"
        MouseArea { anchors.fill: parent }
        Text {
            anchors.centerIn: parent
            text: space.busyText
            font.pixelSize: space.fpx(30)
        }
    }

    // ─── 确认框 (重启进 Android 之类) ────────────────────────────────────
    Rectangle {
        visible: space.confirmApp !== null || space.askText !== ""
        anchors.fill: parent
        z: 10
        color: "#88000000"
        MouseArea { anchors.fill: parent }
        Rectangle {
            anchors.centerIn: parent
            width: Math.min(parent.width - space.px(160), space.px(900))
            height: confirmCol.implicitHeight + space.vpx(96)
            color: space.th.card
            radius: space.th.radius
            ColumnLayout {
                id: confirmCol
                anchors.centerIn: parent
                width: parent.width - 96
                spacing: 40
                Text {
                    Layout.fillWidth: true
                    text: space.askText !== "" ? space.askText
                          : (space.confirmApp ? space.confirmApp.launch.confirm : "")
                    font.pixelSize: space.fpx(30)
                    wrapMode: Text.WordWrap
                }
                RowLayout {
                    Layout.alignment: Qt.AlignRight
                    spacing: 24
                    Kit.SButton {
                        th: space.th
                        ui: space.fs; text: space.st("cancel")
                        visible: !space.askNotice
                        onClicked: { if (space.askText !== "") space.askDone(false); else space.confirmApp = null }
                    }
                    Kit.SButton {
                        th: space.th
                        ui: space.fs; text: space.askNotice ? space.st("got_it") : space.st("ok"); primary: true
                        onClicked: { if (space.askText !== "") space.askDone(true); else space.launch(space.confirmApp) }
                    }
                }
            }
        }
    }
}
