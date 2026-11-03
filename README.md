# reMarkable Space

English · [简体中文](README.zh-CN.md)

reMarkable Space is an app launcher and home screen for supported reMarkable tablets. Its home screen can show
cards supplied by installed apps—such as a plan, weather, or playback status—and its catalog lets users install
and remove apps from the device.

Space is installed alongside a base layer that integrates it with the reMarkable interface. It does not replace the
device's system image, but installation changes the device's boot integration. This is an independent project, not
an official reMarkable product.

## Screenshots

| Home | Weather | Planner |
|---|---|---|
| ![Home](docs/photos/space-1017.jpg) | ![Weather](docs/photos/weather-1017.jpg) | ![Planner](docs/photos/plan-1017.jpg) |

| Music | Podcasts | Radio |
|---|---|---|
| ![Music](docs/photos/music-1017.jpg) | ![Podcasts](docs/photos/podcast-1017.jpg) | ![Radio](docs/photos/radio-1017.jpg) |

## Install

Use the **Space Assistant**. Connect the tablet to your computer by USB; the assistant installs Space and the base
layer it requires together.

You need the tablet's SSH password, shown on the tablet under **Settings → Help → Copyrights and licenses**.

| Assistant | Download |
|---|---|
| macOS | [space-assistant-mac.zip](https://github.com/boangs/remarkable-space/releases/latest/download/space-assistant-mac.zip) |
| Windows | [space-assistant-windows.exe](https://github.com/boangs/remarkable-space/releases/latest/download/space-assistant-windows.exe) |

Verify the download against `SHA256SUMS` in the [latest release](https://github.com/boangs/remarkable-space/releases/latest).

During installation the assistant downloads a bundle of about 140 MB. The download source can be changed in
**Settings → Download source**; if one source fails, the assistant tries the other and verifies downloaded files
before installation.

Supported devices: **reMarkable 2, Paper Pro, and Paper Pro Move**. On macOS, if the first launch reports that the
developer cannot be verified, choose **Open Anyway** in **System Settings → Privacy & Security**.

## After a reMarkable system update

After the first boot following a reMarkable system update, the stock interface may appear without Space. This is
expected while Space adjusts to the updated system in the background.

- **Wait 1–2 minutes without taking action.**
- When the adjustment finishes, **the screen flashes and the interface restarts once**.
- Do not reboot the tablet, reinstall Space, or reflash the device during this process; interrupting it can prevent
  the device from booting.

This should occur only on the first boot after the system update.

**Still on the stock interface after five minutes, or Space returns but installed apps are missing:** connect with the
assistant and reinstall Space. Installs made before v1.0.6 can lose apps after a system update; after an install
using v1.0.6 or later, future updates recover them automatically.

**The device is stuck at boot or repeatedly restarts:** connect it by USB, open the assistant, expand **Device won't
boot**, enter the SSH password, and select **Emergency repair**. The repair restores the stock boot configuration;
your notes are not touched, and you can reinstall Space afterwards.

## Uninstall

The assistant's **Uninstall** action removes Space and its base layer and restores the stock boot configuration.
Notes are not touched. Individual catalog apps can also be removed on the device.

## Architecture and repository layout

Space provides the app shell, app registry, package installation and removal, and lifecycle management for app
background services. It began as part of the now-archived [rmkit-cn](https://github.com/boangs/rmkit) project; the
base layer moved into this repository on 2026-10-20.

| Path | Role |
|---|---|
| `base/` | Integration base: injection chain, system localisation, IME backend, and post-update recovery scripts. See [base/README.md](base/README.md). |
| `shell/` | QML shell (`Space.qml`): tabs, widget mounting, app creation and teardown, and the `space` API exposed to apps. |
| `kit/` | Shared QML components and icons. |
| `apps/` | Built-in system-entry apps deployed by the installer. Third-party apps are installed from the catalog and are maintained elsewhere. |
| `internal/space/` | Go implementation of the app registry, app installation/removal, background-service management, recently-read data, and weather-map rendering. |
| `cmd/space-server/` | Entry point for the Space server. |
| `assistant/` | Desktop installer, updater, uninstaller, emergency-repair, and diagnostic tooling. |

## App model

- The shell does not contain app-specific logic. Apps describe themselves in `manifest.json`; the shell reads the
  registry.
- Apps are isolated from one another and use the shell-provided `space` API to communicate.
- The UI is designed for e-ink: backgrounds are pure white, with colour reserved for small solid elements.
- In development mode, the shell adds cache-busting revisions to local QML URLs. App resources should be built with
  `space.bust(url)` so changed assets are reloaded.

## Manifest example

The manifest schema has not changed for localization. `name` and `version` are required; `name_en` and
`description_en` are optional English fallbacks. The shell follows the tablet's current interface language and
exposes it to apps as `space.lang`. Shell translations live in `shell/i18n/`; an app can provide UI translations in
`i18n/<lang>.json` (with `i18n/en.json` as the fallback). App dictionaries may also localize the reserved keys
`_app_name`, `_app_description`, and `_launch_confirm`. These translations are files, not additional manifest
fields.

```jsonc
{
  "id": "weather",              // lowercase alphanumerics, unique
  "name": "天气",
  "name_en": "Weather",
  "version": "1.0.0",          // required
  "description": "天气预报",
  "description_en": "Weather forecast",
  "entry": "main.qml",          // QML entry point; without it, use launch
  "widget": "widget.qml",       // optional home-screen widget
  "widget_size": "hero|half",
  "widget_slot": "plan",        // optional fixed home-screen position
  "arch": ["aarch64", "armv7"], // empty = all supported architectures
  "service": { "exec": "...", "port": 9100, "keepalive": true }
}
```

## Project links

- [Latest release](https://github.com/boangs/remarkable-space/releases/latest)
- [Archived predecessor: rmkit-cn](https://github.com/boangs/rmkit)
- [Base-layer documentation](base/README.md)

## License

[GNU GPL v3.0](LICENSE). Releases up to and including v1.0.10 were published under Apache-2.0 and remain available
under that license. Third-party code is listed in [base/NOTICE.md](base/NOTICE.md).
