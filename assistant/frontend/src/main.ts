import './style.css'
import { deviceArt } from './device'
import {
  Connect, Disconnect, Probe, ChooseBundle, DownloadBundle, PlanSpace, RunSpace, UninstallSpace,
  PlanAndroid, RunAndroid, UninstallAndroid, BootAndroid, ReturnToStock, ResetAndroidData, ChooseAPKs, InstallAPKs, Cancel, EmergencyRepair, ExportDiagnostics, OpenLogDir, LogDir, DetectProxy, Confirm, LoadPassword, ForgetPassword, DiagnoseAndroid, Version, RepairPanel, Release, LatestVersion, SetLanguage,
} from '../wailsjs/go/main/App'
import { EventsOn, WindowSetTitle } from '../wailsjs/runtime/runtime'

// 与 Go 端结构一一对应 (wailsjs/go/models.ts 里也有生成的类型, 这里只声明用到的字段)
type Info = {
  arch: string; fwVersion: string; imgVersion?: string; resolution: string; model: string; modelKey: string
  activeSlot: string; errcntA: number; errcntB: number; secboot: string; hasLSE: boolean
  rootFreeMB: number; homeFreeMB: number; haveXovi: boolean; rmkitInstalled: boolean; rmkitFW: string
  spaceInstalled: boolean; spaceActive: boolean; spaceVersion: string; androidVersion: string
  xochitlActive: boolean; inAndroidMode: boolean; androidInstalled: boolean; initIsWrapper: boolean
}
type BundleInfo = { path: string; component: string; version: string; created: string; notes: string; files: number; bytes: number }
type FileEntry = { device: string; source: string; mode: number; size: number }
type SpacePlan = { arch: string; fwVersion: string; needXovi: boolean; deployRime: boolean; qmlInject: boolean; librarian: boolean; files: FileEntry[]; totalBytes: number; warnings: string[] }
type AndroidPlan = { fwVersion: string; slot: string; hasSystemPkg: boolean; replaceSystem: boolean; systemPresent: boolean; files: string[]; totalBytes: number; warnings: string[]; blockers: string[] }

type Action = 'space' | 'android'

// —— 文案: 中英各一份, 键名相同 ——————————————————————————
// 界面语言按系统语言定 (中文系统用中文, 其余英文), 设置里可以改。Go 端的日志、错误和
// 设备端脚本的输出也跟着这个开关走 (SetLanguage)。{0} {1} 是占位符。
type Lang = 'zh' | 'en'
const DICT = {
  zh: {
    title: '空间助手', settings: '设置',
    connect_h2: '用 USB 线把设备连上', ssh_pw: 'SSH 密码', pw_ph: '在设备上查', connect: '连接',
    pw_note: '密码在设备的 设置 → 帮助 → 版权与许可 最底部。',
    pw_tip: '用 USB 线连上后设备默认地址就是 10.11.99.1。Windows 首次连接可能要先装 reMarkable 的 USB 网卡驱动。Android 模式下的密码和 reMarkable 系统的一样。',
    remember: '记住密码', remember_ssh: '记住 SSH 密码',
    remember_tip: '存进系统钥匙串（Mac 钥匙串 / Windows 凭据管理器），不写进任何文件。',
    cant_connect: '连不上', host: '地址', forget_pw: '忘记已存的密码',
    wont_boot: '设备起不来',
    rescue_note: '卡在开机画面、反复重启时用。填好上面的 SSH 密码，点「紧急修复」，保持 USB 连着并让设备开机或重启；助手会一直尝试连接，一连上就停用中文、空间等全部增强，让设备以原厂状态启动。之后可以用助手重新安装。',
    rescue: '紧急修复',
    firmware: '固件', unknown: '未知', arch: '架构', slot: '启动槽', errcnt: '错误计数', no_slot: '该机型无 A/B 槽',
    free: '剩余空间', sys_part: '系统分区',
    connected: '已连接，', in_android: '当前在 Android', rm_running: 'reMarkable 系统运行中', rm_not_running: 'reMarkable 系统未运行',
    direct_tip: 'USB 线直连，不经过服务器，不读取你的笔记。',
    reprobe: '重新检测', disconnect: '断开', export_diag: '导出诊断',
    export_tip: '设备切槽、开不了机、空间突然没了时用：只读收集版本、启动配置和最近几次开机的系统日志，存成一个文件发给开发者。不收集笔记和账号信息。',
    space: '空间', update: '更新', reinstall: '重新安装', install: '安装',
    installed: '已安装', up_to_date: '已是最新', can_update: '可更新到', not_installed: '未安装，下载 {0}',
    approx_space: '约 135 MB', approx_android: '约 600 MB',
    dl_update: '下载并更新', dl_install: '下载并安装',
    space_desc: '中文界面、输入法、应用商店', android_desc: '双系统启动，开机时选进哪一个',
    bundle: '安装包', uninstall: '卸载', from_local: '从本地安装',
    replace_sys: '覆盖已有系统', replace_tip: '重新解包 Android 系统到 /home，应用数据目录不受影响。',
    details: '详情',
    safety_space: '写入前会按你的固件重新生成注入表，全部命中才动系统配置；启动后观察 10 秒，任一步不对自动退回出厂启动。',
    safety_android: '出厂内核链接不动，Android 系统与数据都在 /home，重启不切槽。',
    choose_bundle: '选择本地安装包', preview_plan: '预览将写入的内容', maintain: '维护',
    repair_btn: '恢复侧栏入口', repair_tip: '侧栏入口或中文突然消失时用：清除防砖熔断并重启一次 xochitl',
    switch_sys: '切换系统', boot_android: '重启进 Android', back_stock: '回 reMarkable 系统',
    need_android: '需要设备在 Android 模式', install_apk: '安装 APK', boot_diag: '启动诊断',
    reset_data: '重置数据', reset_tip: '清空 Android 的应用与设置，下次进 Android 重新首次开机',
    remove_data: '连数据一起删', remove_tip: '卸载时连同 /home 里的 Android 系统与应用数据一起删除，不勾选则只移除启动组件。',
    log: '日志', cancel: '取消', open_logdir: '打开日志目录',
    dl_source: '下载来源', dl_source_tip: '先从这里下安装包，下不动会自动换另一个。两处是同一个包，下完都会校验。',
    src_cn: '国内下载', src_gh: 'GitHub 下载',
    proxy: '下载代理', proxy_tip: '一般不用填。两处都下不动时才需要，格式如 http://127.0.0.1:7890', proxy_ph: '一般不用填',
    language: '语言', done: '完成',
    f_need_xovi: '设备没有 xovi，会先自动部署', f_have_xovi: 'xovi 已在',
    f_rime: '整句输入（librime，拼音加五笔86）', f_go_ime: '纯 Go 输入引擎（安装包无 librime）',
    f_qml: '运行时 QML 注入', f_qmd: 'qmd 注入', f_librarian: '文件热导入 librarian', f_no_librarian: '不装 librarian（rm2）',
    writes: '写入 <span class="qty">{0}</span> 个文件，共 {1}', writes_slot: '写入 <span class="qty">{0}</span> 个文件，共 {1}，本槽 {2}',
    file_list: '文件清单',
    has_sys_keep: '设备已有 Android 系统，本次不覆盖。', has_sys_extract: '包含 Android 系统包，解包到 /home。', no_sys: '安装包不含系统包，沿用设备上已有的。',
    android_tip: '/boot 里新增 android 内核（出厂内核链接不动）、/lib/modules 新增一个模块目录、/usr/bin 四个宿主程序、/sbin/init 换成带回落的包装脚本（原链接备份为 /sbin/init.systemd-orig）',
    proxy_detected: '检测到系统代理 {0}，若直连下不动，可在设置里填上再试',
    confirm: '确认', what_space: '「空间」及其注入底座', what_android: '单槽 Android',
    confirm_update: '确定要在这台设备上更新 {0} 吗？过程中请不要拔线。', confirm_install: '确定要在这台设备上安装 {0} 吗？过程中请不要拔线。',
    read_pw: '已从钥匙串读到 {0} 的密码', forgot: '已忘记保存的密码', need_pw: '请先填 SSH 密码',
    rescue_no_pw: '没填 SSH 密码: 设备开机早期多半只能用密码登录，建议先填上',
    rescue_confirm: '会停用中文、空间等全部增强，让设备以原厂状态启动（笔记不受影响）。之后可以用助手重新安装。继续？',
    wrong_bundle: '选的是「{0}」的安装包，不是{1}的',
    uninstall_space_confirm: '确定卸载空间及其注入底座？设备会恢复出厂启动配置。',
    uninstall_android_data: '确定卸载 Android 并删除 /home 里的系统与数据？', uninstall_android_keep: '确定卸载 Android（保留 /home 数据）？',
    boot_android_confirm: '设备将重启进入 Android，约 1 到 3 分钟。回来时可在 Android 桌面点“原厂系统”。继续？',
    diag_head: '—— Android 启动诊断 ——', diag_tail: '—— 诊断结束，已存进日志目录，请把该文件发给开发者 ——',
    reset_confirm: '清空 Android 的全部应用与设置？下次进 Android 会重新首次开机（约 5 分钟）。',
    read_saved_pw: '已从钥匙串读到保存的密码', logdir: '日志目录 {0}', version: '空间助手 {0}',
  },
  en: {
    title: 'Space Assistant', settings: 'Settings',
    connect_h2: 'Connect the device over USB', ssh_pw: 'SSH password', pw_ph: 'shown on the device', connect: 'Connect',
    pw_note: 'The password is on the device under Settings → Help → Copyrights and licenses, at the very bottom.',
    pw_tip: 'Over USB the device is always at 10.11.99.1. On Windows the first connection may need the reMarkable USB network driver. In Android mode the password is the same as on the reMarkable system.',
    remember: 'Remember password', remember_ssh: 'Remember SSH password',
    remember_tip: 'Stored in the system keychain (macOS Keychain / Windows Credential Manager), never written to a file.',
    cant_connect: "Can't connect", host: 'Address', forget_pw: 'Forget saved password',
    wont_boot: "Device won't boot",
    rescue_note: 'For a device stuck on the boot screen or rebooting in a loop. Enter the SSH password above, click "Emergency repair", keep USB connected and let the device boot or reboot; the assistant keeps trying to connect and, as soon as it gets in, disables Space and every other add-on so the device boots stock. You can reinstall with the assistant afterwards.',
    rescue: 'Emergency repair',
    firmware: 'Firmware', unknown: 'unknown', arch: 'Architecture', slot: 'Boot slot', errcnt: 'error count', no_slot: 'no A/B slots on this model',
    free: 'Free space', sys_part: 'system partition',
    connected: 'Connected, ', in_android: 'now in Android', rm_running: 'reMarkable system running', rm_not_running: 'reMarkable system not running',
    direct_tip: 'Direct USB connection, no server involved, your notes are never read.',
    reprobe: 'Re-detect', disconnect: 'Disconnect', export_diag: 'Export diagnostics',
    export_tip: 'For slot switches, boot failures or Space suddenly missing: collects versions, boot configuration and the last few boots\' system logs (read-only) into one file to send to the developer. No notes or account data.',
    space: 'Space', update: 'Update', reinstall: 'Reinstall', install: 'Install',
    installed: 'Installed', up_to_date: 'up to date', can_update: '→', not_installed: 'Not installed, downloads {0}',
    approx_space: 'about 135 MB', approx_android: 'about 600 MB',
    dl_update: 'Download and update', dl_install: 'Download and install',
    space_desc: 'Home screen, app catalog, Chinese UI and input method', android_desc: 'Dual boot, pick a system at startup',
    bundle: 'bundle', uninstall: 'Uninstall', from_local: 'Install from file',
    replace_sys: 'Replace existing system', replace_tip: 'Re-extracts the Android system into /home; app data is untouched.',
    details: 'Details',
    safety_space: 'Before writing, the injection table is regenerated for your firmware and the system config is only touched if every entry matches; after start it watches for 10 s and rolls back to stock boot if anything is off.',
    safety_android: 'The stock kernel link is untouched; the Android system and data live in /home; no slot switching.',
    choose_bundle: 'Choose local bundle', preview_plan: 'Preview what will be written', maintain: 'Maintenance',
    repair_btn: 'Restore sidebar entry', repair_tip: 'When the sidebar entry or Chinese UI vanishes: clears the safety fuse and restarts xochitl once',
    switch_sys: 'Switch system', boot_android: 'Reboot into Android', back_stock: 'Back to reMarkable',
    need_android: 'Requires the device to be in Android mode', install_apk: 'Install APK', boot_diag: 'Boot diagnostics',
    reset_data: 'Reset data', reset_tip: 'Wipes Android apps and settings; the next Android boot is a fresh first boot',
    remove_data: 'Also delete data', remove_tip: 'Also deletes the Android system and app data in /home on uninstall; unchecked removes only the boot components.',
    log: 'Log', cancel: 'Cancel', open_logdir: 'Open log folder',
    dl_source: 'Download source', dl_source_tip: 'Bundles are fetched from here first; the other source is tried automatically if it fails. Both serve the same file and it is verified after download.',
    src_cn: 'China mirror', src_gh: 'GitHub',
    proxy: 'Download proxy', proxy_tip: 'Usually not needed. Only if both sources fail, e.g. http://127.0.0.1:7890', proxy_ph: 'usually empty',
    language: 'Language', done: 'Done',
    f_need_xovi: 'No xovi on the device, it will be deployed first', f_have_xovi: 'xovi present',
    f_rime: 'Sentence input (librime, Pinyin + Wubi86)', f_go_ime: 'Pure Go input engine (bundle has no librime)',
    f_qml: 'Runtime QML injection', f_qmd: 'qmd injection', f_librarian: 'librarian hot import', f_no_librarian: 'librarian not installed (rm2)',
    writes: 'Writes <span class="qty">{0}</span> files, {1} total', writes_slot: 'Writes <span class="qty">{0}</span> files, {1} total, slot {2}',
    file_list: 'File list',
    has_sys_keep: 'Android system already present, not replaced this time.', has_sys_extract: 'Includes the Android system package, extracted into /home.', no_sys: 'Bundle has no system package; the one on the device is kept.',
    android_tip: 'Adds an android kernel in /boot (stock kernel link untouched), one module directory in /lib/modules, four host programs in /usr/bin, and replaces /sbin/init with a wrapper that falls back (original link backed up as /sbin/init.systemd-orig)',
    proxy_detected: 'System proxy {0} detected; if the direct download fails, enter it in Settings and retry',
    confirm: 'Confirm', what_space: 'Space and its injection base', what_android: 'single-slot Android',
    confirm_update: 'Update {0} on this device? Do not unplug during the process.', confirm_install: 'Install {0} on this device? Do not unplug during the process.',
    read_pw: 'Loaded the password for {0} from the keychain', forgot: 'Saved password forgotten', need_pw: 'Enter the SSH password first',
    rescue_no_pw: 'No SSH password: early in boot only password login usually works, better fill it in',
    rescue_confirm: 'This disables Space and all other add-ons so the device boots stock (notes are not affected). You can reinstall afterwards. Continue?',
    wrong_bundle: 'That is a "{0}" bundle, not {1}',
    uninstall_space_confirm: 'Uninstall Space and its injection base? The device returns to the stock boot configuration.',
    uninstall_android_data: 'Uninstall Android and delete the system and data in /home?', uninstall_android_keep: 'Uninstall Android (keep /home data)?',
    boot_android_confirm: 'The device will reboot into Android (1 to 3 minutes). To come back, tap "Stock system" on the Android home screen. Continue?',
    diag_head: '—— Android boot diagnostics ——', diag_tail: '—— Done; saved to the log folder, please send that file to the developer ——',
    reset_confirm: 'Wipe all Android apps and settings? The next Android boot is a fresh first boot (about 5 minutes).',
    read_saved_pw: 'Loaded the saved password from the keychain', logdir: 'Log folder {0}', version: 'Space Assistant {0}',
  },
} as const
type Key = keyof typeof DICT.zh

function detectLang(): Lang {
  try {
    const saved = localStorage.getItem('ui.lang')
    if (saved === 'zh' || saved === 'en') return saved
  } catch { /* 忽略 */ }
  return (navigator.language || '').toLowerCase().startsWith('zh') ? 'zh' : 'en'
}
let lang: Lang = detectLang()
const t = (k: Key, ...args: (string | number)[]): string => {
  let s: string = DICT[lang][k]
  args.forEach((a, i) => { s = s.split(`{${i}}`).join(String(a)) })
  return s
}
// 动词接名词: 中文不留空格 ("更新空间"), 但接英文词要留 ("更新 Android"); 英文永远留。
const verbName = (verb: string, name: string) => lang === 'zh' ? verb + (/^[A-Za-z]/.test(name) ? ' ' : '') + name : verb + ' ' + name

function applyLang(l: Lang) {
  lang = l
  try { localStorage.setItem('ui.lang', l) } catch { /* 忽略 */ }
  document.documentElement.lang = l === 'zh' ? 'zh-CN' : 'en'
  document.title = t('title')
  try { WindowSetTitle(t('title')) } catch { /* 浏览器里预览时没有 runtime */ }
  SetLanguage(l).catch(() => { /* 开发版 */ })
}

const state = {
  info: null as Info | null,
  // 两种安装包各记各的。以前共用一个, 切到 Android 时会拿装空间用过的包来说
  // "已选的是 space 的包, 不匹配" —— 可用户在 Android 这边根本没选过东西。
  bundles: { space: null as BundleInfo | null, android: null as BundleInfo | null },
  busy: false,
  busyOn: null as Action | null,
  open: { space: false, android: false },
  settings: false,
  replaceSystem: false,
  removeData: false,
  proxy: '',
  source: 'cn' as Source,   // 安装包从哪下: 国内镜像 / GitHub (另一个当后备)
  version: '',
  remember: true,
  password: '',
}

// 安装包两处都有: GitHub Release (仓库公开) 和国内镜像 (GitHub 在国内常下不动)。用户在设置里
// 选先从哪下, 下不动自动换另一处。空间固定走 latest, 不再要求每次发版都重打助手;
// 只有 latest/space-version.txt 读不到时, 才用构建时注入的发布标签做显示兜底。
// 不写死整包 sha256: 同版本替换安装包时, 已经发出去的助手照样能下能装。
// Android 包几个月才变一次, 不跟着每版重传, 单独记它放在哪个发布标签下 (androidTag)。
// 自己的服务器 (boangs.com) 留作最后的后备: 旧版助手只认那里。
type Source = 'cn' | 'github'
// spaceLatest: 安装包旁 space-version.txt 里的版本 —— 空间在设备上自更新, 发新版时安装包原地换新,
// 助手不跟着发版; 读不到就用发布标签
type Release = { tag: string; cnBase: string; androidTag: string; androidVer: string; spaceLatest?: string }
let release: Release = { tag: '', cnBase: '', androidTag: '', androidVer: '' }
const GITHUB = 'https://github.com/boangs/remarkable-space/releases/'
const OWN_SERVER = 'https://boangs.com/space/'
const BUNDLE_NAME: Record<Action, string> = { space: 'space-bundle.zip', android: 'android-rmppm-bundle.zip' }
function bundleURLs(a: Action): string[] {
  const name = BUNDLE_NAME[a]
  // 空间永远走 latest: 新正式版发布后, 老助手也能发现最新版并下载, 不必跟着重打助手。
  // Android 仍按自己的发布标签取包 (大版本独立维护)。
  const tag = a === 'android' ? release.androidTag : ''
  const gh = GITHUB + (tag ? 'download/' + tag + '/' : 'latest/download/') + name
  const cn = release.cnBase ? release.cnBase + (tag || 'latest') + '/' + name : ''
  const order = state.source === 'github' ? [gh, cn] : [cn, gh]
  if (a === 'android') order.push(OWN_SERVER + name)
  return order.filter((u) => u !== '')
}
function versionURLs(): string[] {
  return bundleURLs('space').map((u) => u.replace(/space-bundle\.zip$/, 'space-version.txt'))
}
// 比版本号 (和空间外壳 versionCmp 一致): 按数字逐段比, 带 "-" 预发布后缀的低于同号正式版; a 新返回正数。
// 只比相不相等的话, 设备自更新到 v1.1.5 后助手会喊「可更新到 v1.1.0」, 一点就降级。
function numCmp(a: string, b: string): number {
  const x = a.match(/\d+/g) || [], y = b.match(/\d+/g) || []
  for (let i = 0; i < Math.max(x.length, y.length); i++) {
    const d = parseInt(x[i] || '0', 10) - parseInt(y[i] || '0', 10)
    if (d !== 0) return d
  }
  return 0
}
function versionCmp(a: string, b: string): number {
  const pa = a.split('-'), pb = b.split('-')
  const d = numCmp(pa[0], pb[0])
  if (d !== 0) return d
  if (pa.length === 1 && pb.length > 1) return 1
  if (pa.length > 1 && pb.length === 1) return -1
  return numCmp(pa.slice(1).join('-'), pb.slice(1).join('-'))
}
const COMPONENT: Record<Action, string> = { space: 'space', android: 'android-rmppm' }
const approx = (a: Action) => a === 'space' ? t('approx_space') : t('approx_android')
const label = (a: Action) => a === 'space' ? t('space') : 'Android'

const $ = (sel: string) => document.querySelector(sel) as HTMLElement
const mb = (n: number) => (n / 1e6).toFixed(1) + ' MB'
const num = (n: number) => n.toLocaleString('en-US')
const esc = (s: string) => s.replace(/[&<>"]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c] as string))
// 机器串 (路径、版本哈希、固件戳) 走等宽; 人话永远不走。
const mono = (s: string) => `<span class="mono">${esc(s)}</span>`

// Android 只在 Paper Pro Move 上验证过, 别的机型连这一行都不该出现。
const androidOK = (i: Info | null) => !!i && i.modelKey === 'rmppm'
const CODENAME: Record<string, string> = { rmppm: 'Chiappa', rmpp: 'Ferrari', rm2: 'Zero Sugar' }

const ICON = {
  gear: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7"><circle cx="12" cy="12" r="3.2"/><path d="M19.4 14a1.6 1.6 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.6 1.6 0 0 0-1.8-.3 1.6 1.6 0 0 0-1 1.5V20a2 2 0 1 1-4 0v-.1a1.6 1.6 0 0 0-1-1.5 1.6 1.6 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.6 1.6 0 0 0 .3-1.8 1.6 1.6 0 0 0-1.5-1H4a2 2 0 1 1 0-4h.1a1.6 1.6 0 0 0 1.5-1 1.6 1.6 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.6 1.6 0 0 0 1.8.3H10a1.6 1.6 0 0 0 1-1.5V4a2 2 0 1 1 4 0v.1a1.6 1.6 0 0 0 1 1.5 1.6 1.6 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.6 1.6 0 0 0-.3 1.8V10a1.6 1.6 0 0 0 1.5 1H20a2 2 0 1 1 0 4h-.1a1.6 1.6 0 0 0-1.5 1Z"/></svg>`,
  info: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9"><circle cx="12" cy="12" r="9"/><path d="M12 11v5" stroke-linecap="round"/><circle cx="12" cy="7.8" r=".9" fill="currentColor" stroke="none"/></svg>`,
  chev: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m9 6 6 6-6 6"/></svg>`,
}

// 说明不往界面上堆: 做成一个 ⓘ, 要看的人悬停自然看得到。
const tip = (text: string) => `<span class="tip" title="${esc(text)}">${ICON.info}</span>`

function render() {
  const i = state.info
  $('#app').innerHTML = `
    <header class="masthead">
      <h1>${t('title')}</h1>
      ${i ? `<button id="btn-settings" class="icon" title="${t('settings')}">${ICON.gear}</button>` : ''}
    </header>
    ${i ? renderDevice(i) : renderConnect()}
    ${i ? renderItems(i) : ''}
    ${renderLog()}
    ${state.settings ? renderSettings() : ''}
  `
  bind()
}

// —— 没连设备 ——————————————————————————————————————

function renderConnect() {
  return `
    <section class="stage">
      ${deviceArt({ connected: false, space: false, android: false, inAndroid: false })}
      <div class="stagebody">
        <h2>${t('connect_h2')}</h2>
        <div class="field">
          <label for="password">${t('ssh_pw')}</label>
          <input id="password" type="password" value="${esc(state.password)}" placeholder="${t('pw_ph')}" autocomplete="off">
          <button id="btn-connect" class="go">${t('connect')}</button>
        </div>
        <p class="note">${t('pw_note')}${tip(t('pw_tip'))}</p>
        <label class="opt"><input type="checkbox" id="chk-remember" ${state.remember ? 'checked' : ''}> ${t('remember')}${tip(t('remember_tip'))}</label>
        <details class="fallback">
          <summary>${t('cant_connect')}</summary>
          <div class="field">
            <label for="host">${t('host')}</label>
            <input id="host" value="10.11.99.1">
            <button id="btn-forget" class="flat" ${state.password ? '' : 'disabled'}>${t('forget_pw')}</button>
          </div>
        </details>
        <details class="fallback">
          <summary>${t('wont_boot')}</summary>
          <p class="note">${t('rescue_note')}</p>
          <div class="field">
            <button id="btn-rescue" class="peril" ${state.busy ? 'disabled' : ''}>${t('rescue')}</button>
          </div>
        </details>
        <p class="note lang"><a href="#" id="lnk-lang">${lang === 'zh' ? 'English' : '中文'}</a></p>
      </div>
    </section>
  `
}

// —— 设备 ——————————————————————————————————————

function renderDevice(i: Info) {
  const specs: [string, string][] = [
    [t('firmware'), mono(i.imgVersion ? `${i.imgVersion} (${i.fwVersion})` : (i.fwVersion || t('unknown')))],
    [t('arch'), `${mono(i.arch)}${CODENAME[i.modelKey] ? `<span class="dim">（${CODENAME[i.modelKey]}）</span>` : ''}`],
    [t('slot'), i.activeSlot ? `${mono(i.activeSlot)}<span class="dim">　${t('errcnt')} ${mono(`a=${i.errcntA} b=${i.errcntB}`)}</span>` : `<span class="dim">${t('no_slot')}</span>`],
    [t('free'), `${t('sys_part')} <span class="qty">${num(i.rootFreeMB)}</span> MB<span class="dim">　/home <span class="qty">${num(i.homeFreeMB)}</span> MB</span>`],
  ]
  const status = i.inAndroidMode ? t('in_android') : (i.xochitlActive ? t('rm_running') : t('rm_not_running'))
  return `
    <section class="stage">
      ${deviceArt({ connected: true, space: i.spaceInstalled, android: i.androidInstalled, inAndroid: i.inAndroidMode })}
      <div class="stagebody">
        <h2>${esc(i.model)}</h2>
        <p class="live"><span class="dot"></span>${t('connected')}${status}${tip(t('direct_tip'))}</p>
        <dl class="spec">${specs.map(([k, v]) => `<dt>${k}</dt><dd>${v}</dd>`).join('')}</dl>
        <div class="field">
          <button id="btn-probe" class="flat" ${state.busy ? 'disabled' : ''}>${t('reprobe')}</button>
          <button id="btn-disconnect" class="flat" ${state.busy ? 'disabled' : ''}>${t('disconnect')}</button>
          <button id="btn-export-diag" class="flat" ${state.busy ? 'disabled' : ''} title="${esc(t('export_tip'))}">${t('export_diag')}</button>
        </div>
      </div>
    </section>
  `
}

// —— 可安装的东西: 账本式的行, 不是卡片 ——————————————————

function renderItems(i: Info) {
  return `<div class="ledger">${renderItem(i, 'space')}${androidOK(i) ? renderItem(i, 'android') : ''}</div>`
}

function renderItem(i: Info, a: Action) {
  const isSpace = a === 'space'
  const b = state.bundles[a]
  const ready = !!b && b.component === COMPONENT[a]
  const installed = isSpace ? i.spaceInstalled : i.androidInstalled
  const busy = state.busy ? 'disabled' : ''
  // 设备上记的版本 vs 线上最新版本: 不比它旧就是「已是最新」(设备自更新得比助手知道的还新也算)。
  // 设备上没记版本 (老版助手装的 Android) 就判断不了, 只给「重新安装」, 不说有更新
  const cur = isSpace ? (i.spaceVersion || '') : (i.androidVersion || '')
  const latest = isSpace ? (release.spaceLatest || release.tag) : release.androidVer
  const upToDate = installed && cur !== '' && latest !== '' && versionCmp(cur, latest) >= 0
  const unknown = installed && (cur === '' || latest === '')
  const outdated = installed && !upToDate && !unknown
  const title = verbName(outdated ? t('update') : installed ? t('reinstall') : t('install'), label(a))

  let stateCell: string
  if (upToDate) stateCell = `<span class="on">${t('installed')}</span> ${mono(cur)}　${t('up_to_date')}`
  else if (outdated) stateCell = `<span class="on">${t('installed')}</span> ${mono(cur)}　${t('can_update')} ${mono(latest)}`
  else if (installed && cur) stateCell = `<span class="on">${t('installed')}</span> ${mono(cur)}`
  else if (installed) stateCell = `<span class="on">${t('installed')}</span>`
  else stateCell = `<span class="dim">${t('not_installed', approx(a))}</span>`
  // 主按钮: 要装 / 要更新才是显眼的黑底; 已是最新 (或判断不了) 时「重新安装」只是个普通按钮, 用来修复
  const runClass = (installed && !outdated) ? 'flat' : 'go'
  const runText = ready || (installed && !outdated) ? title : (outdated ? t('dl_update') : t('dl_install'))

  return `
    <section class="item ${state.busyOn === a ? 'working' : ''}">
      <div class="itemrow">
        <div class="what">
          <h3>${label(a)}</h3>
          <p>${isSpace ? t('space_desc') : t('android_desc')}</p>
        </div>
        <div class="stat">${stateCell}${ready ? `<span class="pending">${t('bundle')} ${mono(b!.version)}　${mb(b!.bytes)}</span>` : ''}</div>
        <div class="acts">
          <button class="${runClass} act-run" data-a="${a}" ${busy}>${runText}</button>
          ${installed
            ? `<button class="peril ${isSpace ? 'act-uninstall' : 'act-uninstall-android'}" data-a="${a}" ${busy}>${t('uninstall')}</button>`
            : `<button class="flat act-choose" data-a="${a}" ${busy}>${t('from_local')}</button>`}
        </div>
      </div>
      ${!isSpace && installed ? `<label class="opt"><input type="checkbox" class="chk-replace" ${state.replaceSystem ? 'checked' : ''} ${busy}> ${t('replace_sys')}${tip(t('replace_tip'))}</label>` : ''}
      <div class="progress ${state.busyOn === a ? '' : 'hidden'}" id="dl-${a}"><div></div></div>
      <button class="disclose act-more" data-a="${a}" aria-expanded="${state.open[a]}">${ICON.chev}${t('details')}</button>
      ${state.open[a] ? renderDetail(i, a) : ''}
    </section>
  `
}

function renderDetail(i: Info, a: Action) {
  const busy = state.busy ? 'disabled' : ''
  const ready = !!state.bundles[a] && state.bundles[a]!.component === COMPONENT[a]
  const safety = a === 'space' ? t('safety_space') : t('safety_android')
  return `
    <div class="detail">
      <div class="grp">
        <h4>${t('install')}${tip(safety)}</h4>
        <div class="field">
          <button class="flat act-choose" data-a="${a}" ${busy}>${t('choose_bundle')}</button>
          <button class="flat act-plan" data-a="${a}" ${busy || !ready ? 'disabled' : ''}>${t('preview_plan')}</button>
        </div>
      </div>
      ${a === 'space' ? `
        <div class="grp">
          <h4>${t('maintain')}</h4>
          <div class="field">
            <button id="btn-repair" class="flat" ${busy || !i.rmkitInstalled || i.inAndroidMode ? 'disabled' : ''} title="${esc(t('repair_tip'))}">${t('repair_btn')}</button>
          </div>
        </div>` : `
        <div class="grp">
          <h4>${t('switch_sys')}</h4>
          <div class="field">
            <button id="btn-boot-android" class="flat" ${busy || !i.androidInstalled || i.inAndroidMode ? 'disabled' : ''}>${t('boot_android')}</button>
            <button id="btn-stock" class="flat" ${busy || !i.inAndroidMode ? 'disabled' : ''}>${t('back_stock')}</button>
          </div>
        </div>
        <div class="grp">
          <h4>${t('maintain')}</h4>
          <div class="field">
            <button id="btn-apks" class="flat" ${busy || !i.inAndroidMode ? 'disabled' : ''} title="${t('need_android')}">${t('install_apk')}</button>
            <button id="btn-diag" class="flat" ${busy}>${t('boot_diag')}</button>
            <button id="btn-reset-data" class="peril" ${busy || !i.androidInstalled || i.inAndroidMode ? 'disabled' : ''} title="${esc(t('reset_tip'))}">${t('reset_data')}</button>
          </div>
          <label class="opt"><input type="checkbox" id="chk-removedata" ${state.removeData ? 'checked' : ''} ${busy}> ${t('remove_data')}${tip(t('remove_tip'))}</label>
        </div>`}
      <div class="plan" id="plan-${a}"></div>
    </div>
  `
}

// —— 日志 ——————————————————————————————————————

function renderLog() {
  return `
    <section class="journal">
      <div class="jhead">
        <h4>${t('log')}</h4>
        ${state.busy ? `<button id="btn-cancel" class="peril">${t('cancel')}</button>` : ''}
        <button id="btn-logdir" class="flat">${t('open_logdir')}</button>
      </div>
      <pre id="log"></pre>
    </section>
  `
}

// —— 设置 ——————————————————————————————————————

function renderSettings() {
  return `
    <div class="scrim" id="scrim">
      <div class="panel" role="dialog" aria-label="${t('settings')}">
        <h2>${t('settings')}</h2>
        <div class="field stack">
          <label>${t('language')}</label>
          <div class="seg" role="radiogroup">
            <label class="opt"><input type="radio" name="lang" value="zh" ${lang === 'zh' ? 'checked' : ''}> 中文</label>
            <label class="opt"><input type="radio" name="lang" value="en" ${lang === 'en' ? 'checked' : ''}> English</label>
          </div>
        </div>
        <div class="field stack">
          <label>${t('dl_source')}${tip(t('dl_source_tip'))}</label>
          <div class="seg" role="radiogroup">
            <label class="opt"><input type="radio" name="src" value="cn" ${state.source === 'cn' ? 'checked' : ''}> ${t('src_cn')}</label>
            <label class="opt"><input type="radio" name="src" value="github" ${state.source === 'github' ? 'checked' : ''}> ${t('src_gh')}</label>
          </div>
        </div>
        <div class="field stack">
          <label for="proxy">${t('proxy')}${tip(t('proxy_tip'))}</label>
          <input id="proxy" value="${esc(state.proxy)}" placeholder="${t('proxy_ph')}">
        </div>
        <label class="opt"><input type="checkbox" id="chk-remember2" ${state.remember ? 'checked' : ''}> ${t('remember_ssh')}${tip(t('remember_tip'))}</label>
        <div class="field"><button id="btn-forget2" class="flat">${t('forget_pw')}</button></div>
        <div class="field right">
          <span class="ver">${esc(state.version)}</span>
          <button id="btn-close-settings" class="go">${t('done')}</button>
        </div>
      </div>
    </div>
  `
}

// —— 计划预览 ————————————————————————————————————

function renderSpacePlan(p: SpacePlan) {
  const flags = [
    p.needXovi ? t('f_need_xovi') : t('f_have_xovi'),
    p.deployRime ? t('f_rime') : t('f_go_ime'),
    p.qmlInject ? t('f_qml') : t('f_qmd'),
    p.librarian ? t('f_librarian') : t('f_no_librarian'),
  ]
  return `
    <p class="sum">${t('writes', (p.files ?? []).length, mb(p.totalBytes))}</p>
    <ul class="flags">${flags.map((f) => `<li>${f}</li>`).join('')}</ul>
    ${(p.warnings ?? []).map((w) => `<p class="warn">${esc(w)}</p>`).join('')}
    <details><summary>${t('file_list')}</summary><pre class="files">${(p.files ?? []).map((f) => `${esc(f.device)}  ←  ${esc(f.source)}  (${(f.size / 1024).toFixed(0)} KB)`).join('\n')}</pre></details>
  `
}

function renderAndroidPlan(p: AndroidPlan) {
  const sys = p.hasSystemPkg ? (p.systemPresent && !p.replaceSystem ? t('has_sys_keep') : t('has_sys_extract')) : t('no_sys')
  return `
    ${(p.blockers ?? []).map((b) => `<p class="stop">${esc(b)}</p>`).join('')}
    ${(p.warnings ?? []).map((w) => `<p class="warn">${esc(w)}</p>`).join('')}
    <p class="sum">${t('writes_slot', (p.files ?? []).length, mb(p.totalBytes), mono(p.slot))}</p>
    <p class="note">${sys}${tip(t('android_tip'))}</p>
    <details><summary>${t('file_list')}</summary><pre class="files">${(p.files ?? []).map((f) => esc(f)).join('\n')}</pre></details>
  `
}

// —— 杂务 ——————————————————————————————————————

function appendLog(line: string) {
  const el = document.querySelector('#log') as HTMLPreElement | null
  if (!el) return
  const t = new Date().toTimeString().slice(0, 8)
  el.textContent += `${t}  ` + line + '\n'
  el.scrollTop = el.scrollHeight
}

// 整页重绘会把日志 <pre> 一起换掉, 所以每次 render 前后搬一次文本。
function keepLog(fn: () => void) {
  const el = document.querySelector('#log') as HTMLPreElement | null
  const log = el ? el.textContent : ''
  fn()
  const el2 = document.querySelector('#log') as HTMLPreElement | null
  if (el2) { el2.textContent = log; el2.scrollTop = 1e9 }
}

function setBusy(b: boolean, on: Action | null = null) {
  state.busy = b
  state.busyOn = b ? on : null
  keepLog(render)
}

async function guarded(on: Action, fn: () => Promise<void>) {
  if (state.busy) return
  setBusy(true, on)
  try {
    await fn()
  } catch (e) {
    appendLog('✗ ' + String(e))
  } finally {
    try { state.info = (await Probe()) as Info } catch { /* 设备可能已重启 */ }
    setBusy(false)
    // 唯一一处非用户触发的动效: 装完了, 画像像真机那样闪一下重绘。
    const art = document.querySelector('.device')
    if (art) { art.classList.remove('refresh'); void (art as SVGElement).getBoundingClientRect(); art.classList.add('refresh') }
  }
}

// 下载加安装一条龙: 没有包就先下, 有包直接装。
async function runAction(a: Action) {
  const i = state.info as Info
  const have = () => !!state.bundles[a] && state.bundles[a]!.component === COMPONENT[a]
  if (!have()) {
    await guarded(a, async () => {
      try {
        state.bundles[a] = (await DownloadBundle(bundleURLs(a), state.proxy)) as BundleInfo
      } catch (e) {
        // 下不动的时候才提代理。探测到系统代理就告诉他可以去「设置」里填上再试。
        if (!state.proxy) {
          const sys = await DetectProxy().catch(() => '')
          if (sys) appendLog(t('proxy_detected', sys))
        }
        throw e
      }
    })
    if (!have()) return   // 下载失败, 别接着装
  }
  if (a === 'android') {
    const p = (await PlanAndroid(state.replaceSystem).catch((e) => { appendLog('✗ ' + String(e)); return null })) as AndroidPlan | null
    if (!p) return
    if ((p.blockers ?? []).length) { state.open.android = true; keepLog(render); $('#plan-android').innerHTML = renderAndroidPlan(p); return }
  }
  const installed = a === 'space' ? i.spaceInstalled : i.androidInstalled
  const what = a === 'space' ? t('what_space') : t('what_android')
  if (!(await Confirm(t('confirm'), installed ? t('confirm_update', what) : t('confirm_install', what)))) return
  guarded(a, async () => { if (a === 'space') await RunSpace(); else await RunAndroid(state.replaceSystem) })
}

function switchLang(l: Lang) {
  if (l === lang) return
  applyLang(l)
  keepLog(render)
}

function bind() {
  // 语言
  $('#lnk-lang')?.addEventListener('click', (e) => { e.preventDefault(); switchLang(lang === 'zh' ? 'en' : 'zh') })
  document.querySelectorAll('input[name="lang"]').forEach((el) => el.addEventListener('change', (e) => {
    switchLang((e.target as HTMLInputElement).value === 'en' ? 'en' : 'zh')
  }))

  // 连接
  $('#host')?.addEventListener('change', async (e) => {
    const host = (e.target as HTMLInputElement).value.trim()
    const saved = await LoadPassword(host)
    if (saved) { state.password = saved; ($('#password') as HTMLInputElement).value = saved; appendLog(t('read_pw', host)) }
  })
  $('#chk-remember')?.addEventListener('change', (e) => { state.remember = (e.target as HTMLInputElement).checked })
  $('#chk-remember2')?.addEventListener('change', (e) => { state.remember = (e.target as HTMLInputElement).checked })
  $('#btn-forget')?.addEventListener('click', async () => { await ForgetPassword(($('#host') as HTMLInputElement)?.value || '10.11.99.1'); state.password = ''; keepLog(render) })
  $('#btn-forget2')?.addEventListener('click', async () => { await ForgetPassword('10.11.99.1'); state.password = ''; appendLog(t('forgot')) })
  $('#password')?.addEventListener('keydown', (e) => { if ((e as KeyboardEvent).key === 'Enter') $('#btn-connect')?.click() })
  $('#btn-connect')?.addEventListener('click', async () => {
    const host = ($('#host') as HTMLInputElement)?.value || '10.11.99.1'
    const pw = ($('#password') as HTMLInputElement).value
    state.password = pw
    if (!pw) { appendLog(t('need_pw')); return }
    try {
      state.info = (await Connect(host, pw, state.remember)) as Info
      keepLog(render)
    } catch (e) { appendLog('✗ ' + String(e)) }
  })
  $('#btn-rescue')?.addEventListener('click', async () => {
    const host = ($('#host') as HTMLInputElement)?.value || '10.11.99.1'
    const pw = ($('#password') as HTMLInputElement).value
    state.password = pw
    if (!pw) appendLog(t('rescue_no_pw'))
    if (!(await Confirm(t('rescue'), t('rescue_confirm')))) return
    setBusy(true)
    try { await EmergencyRepair(host, pw) } catch (e) { appendLog('✗ ' + String(e)) }
    setBusy(false)
  })
  $('#btn-export-diag')?.addEventListener('click', async () => {
    setBusy(true)
    try { await ExportDiagnostics() } catch (e) { appendLog('✗ ' + String(e)) }
    setBusy(false)
  })
  $('#btn-disconnect')?.addEventListener('click', async () => { await Disconnect(); state.info = null; keepLog(render) })
  $('#btn-probe')?.addEventListener('click', async () => { try { state.info = (await Probe()) as Info; keepLog(render) } catch (e) { appendLog('✗ ' + String(e)) } })

  // 条目
  document.querySelectorAll<HTMLButtonElement>('.act-more').forEach((b) => b.addEventListener('click', () => {
    const a = b.dataset.a as Action
    state.open[a] = !state.open[a]
    keepLog(render)
  }))
  document.querySelectorAll<HTMLButtonElement>('.act-run').forEach((b) => b.addEventListener('click', () => runAction(b.dataset.a as Action)))
  document.querySelectorAll<HTMLButtonElement>('.act-choose').forEach((btn) => btn.addEventListener('click', async () => {
    const a = btn.dataset.a as Action
    try {
      const b = (await ChooseBundle()) as BundleInfo | null
      if (!b) return
      if (b.component !== COMPONENT[a]) { appendLog(t('wrong_bundle', b.component, label(a))); return }
      state.bundles[a] = b
      keepLog(render)
    } catch (e) { appendLog('✗ ' + String(e)) }
  }))
  document.querySelectorAll<HTMLButtonElement>('.act-plan').forEach((b) => b.addEventListener('click', async () => {
    const a = b.dataset.a as Action
    const box = $(`#plan-${a}`)
    try {
      box.innerHTML = a === 'space' ? renderSpacePlan((await PlanSpace()) as SpacePlan) : renderAndroidPlan((await PlanAndroid(state.replaceSystem)) as AndroidPlan)
    } catch (e) { box.innerHTML = `<p class="stop">${esc(String(e))}</p>` }
  }))
  document.querySelectorAll<HTMLButtonElement>('.act-uninstall').forEach((b) => b.addEventListener('click', async () => {
    if (await Confirm(t('confirm'), t('uninstall_space_confirm'))) guarded('space', () => UninstallSpace())
  }))
  document.querySelectorAll<HTMLButtonElement>('.act-uninstall-android').forEach((b) => b.addEventListener('click', async () => {
    if (await Confirm(t('confirm'), state.removeData ? t('uninstall_android_data') : t('uninstall_android_keep'))) guarded('android', () => UninstallAndroid(state.removeData))
  }))
  document.querySelectorAll<HTMLInputElement>('.chk-replace').forEach((b) => b.addEventListener('change', (e) => { state.replaceSystem = (e.target as HTMLInputElement).checked }))
  $('#chk-removedata')?.addEventListener('change', (e) => { state.removeData = (e.target as HTMLInputElement).checked })

  // 详情里的那些
  $('#btn-repair')?.addEventListener('click', () => guarded('space', () => RepairPanel()))
  $('#btn-boot-android')?.addEventListener('click', async () => { if (await Confirm(t('confirm'), t('boot_android_confirm'))) guarded('android', () => BootAndroid()) })
  $('#btn-stock')?.addEventListener('click', () => guarded('android', () => ReturnToStock()))
  $('#btn-diag')?.addEventListener('click', async () => {
    try { const text = await DiagnoseAndroid(); appendLog(t('diag_head') + '\n' + text + t('diag_tail')) } catch (e) { appendLog('✗ ' + String(e)) }
  })
  $('#btn-reset-data')?.addEventListener('click', async () => { if (await Confirm(t('confirm'), t('reset_confirm'))) guarded('android', () => ResetAndroidData()) })
  $('#btn-apks')?.addEventListener('click', async () => {
    let paths: string[] = []
    try { paths = (await ChooseAPKs()) || [] } catch (e) { appendLog('✗ ' + String(e)); return }
    if (!paths.length) return
    guarded('android', () => InstallAPKs(paths))
  })

  // 设置
  $('#btn-settings')?.addEventListener('click', () => { state.settings = true; keepLog(render) })
  $('#btn-close-settings')?.addEventListener('click', () => { state.settings = false; keepLog(render) })
  $('#scrim')?.addEventListener('click', (e) => { if (e.target === $('#scrim')) { state.settings = false; keepLog(render) } })
  document.querySelectorAll('input[name="src"]').forEach((el) => el.addEventListener('change', (e) => {
    state.source = (e.target as HTMLInputElement).value === 'github' ? 'github' : 'cn'
    try { localStorage.setItem('download.source', state.source) } catch { /* 忽略 */ }
  }))
  $('#proxy')?.addEventListener('change', (e) => {
    state.proxy = (e.target as HTMLInputElement).value.trim()
    try { localStorage.setItem('proxy.manual', state.proxy) } catch { /* 忽略 */ }
  })

  $('#btn-cancel')?.addEventListener('click', () => Cancel())
  $('#btn-logdir')?.addEventListener('click', () => { OpenLogDir().catch((e) => appendLog('✗ ' + String(e))) })
}

EventsOn('log', (line: string) => appendLog(line))
EventsOn('progress', (p: { done: number; total: number }) => {
  const a = state.busyOn
  const bar = a ? (document.querySelector(`#dl-${a} div`) as HTMLElement | null) : null
  if (bar && p.total > 0) bar.style.width = Math.min(100, (p.done / p.total) * 100).toFixed(1) + '%'
})

// 代理框默认留空。以前启动时会探测系统代理自动填进去 —— 可各人代理不一样,
// 填错了反而连不上, 不懂的人也不知道那一栏该删。现在只有用户自己填过才记住。
// (key 换过名: 老版本自动探测写进 'proxy' 的值就此失效)
try { state.proxy = localStorage.getItem('proxy.manual') || '' } catch { /* 忽略 */ }
// 下载来源没选过时跟着语言走: 中文默认国内镜像, 英文默认 GitHub
try {
  const src = localStorage.getItem('download.source')
  state.source = src === 'github' || (src === null && lang === 'en') ? 'github' : 'cn'
} catch { /* 忽略 */ }
applyLang(lang)
Release().then(async (r) => {
  release = r as Release
  const v = await LatestVersion(versionURLs(), state.proxy)
  if (v) { release.spaceLatest = v; render() }
}).catch(() => { /* 开发版: 用默认 */ })
render()
LoadPassword('10.11.99.1').then((pw) => { if (pw) { state.password = pw; keepLog(render); appendLog(t('read_saved_pw')) } })
LogDir().then((d) => appendLog(t('logdir', d)))
Version().then((v) => { state.version = v; appendLog(t('version', v)) })
