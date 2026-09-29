<div align="center">
  <br>
  <img src="https://github.com/user-attachments/assets/f302f5d2-730c-44d2-a2c1-63cdd75804f2" width="136" alt="Pastel 图标">
  <h1>Pastel</h1>
  <h3>查找并下载 App 的历史版本</h3>
  <p>在 Mac 上浏览版本记录，从 Apple 下载 IPA，并通过 AirDrop 发送到你的设备。</p>
  <br>
  <p>
    <a href="https://github.com/EEliberto/Pastel-macOS/releases/latest"><strong>下载 Pastel</strong></a>
    &nbsp;&nbsp;·&nbsp;&nbsp;
    <a href="release-notes/20260929/release-notes.md">查看新功能</a>
  </p>
  <p><sub>需要 macOS 26 或更高版本，以及 Apple Silicon Mac。</sub></p>
  <br>
</div>

<p align="center">
  <img width="980" alt="Pastel 主界面" src="https://github.com/user-attachments/assets/690166e2-78ad-42f8-9db2-40b79e435b71">
</p>

<br>

## 找到需要的版本

搜索不同地区的 App Store，查看版本记录，然后选择需要的版本下载。Pastel 会根据 Apple 账户所在地区自动匹配商店，让搜索和下载保持一致。

<p align="center">
  <img width="820" alt="Pastel App 版本详情" src="https://github.com/user-attachments/assets/f3685fee-445f-41bc-8fea-2d1e602dec92">
</p>

<br>

## 更完整的版本记录

Pastel 汇集 Timbrd、Agzy、Bilin 和 Apple 提供的版本信息。无论从哪里找到版本，IPA 都由 Apple 提供。你也可以输入 App ID 和版本 ID，直接下载指定版本。

如果账户尚未获取某个免费 App，Pastel 会在你确认后先将它加入账户，再继续下载。付费 App 需要由该账户事先购买。

<p align="center">
  <img width="560" alt="Pastel 版本来源选择" src="https://github.com/user-attachments/assets/4de67361-8727-4705-8718-f9be81bc7b01">
</p>

<br>

## 集中管理下载

下载资料库集中显示 IPA 的版本、地区和 Apple 账户信息。使用系统分享菜单或 AirDrop，即可将文件发送到 iPhone 或 iPad。

<p align="center">
  <img width="900" alt="Pastel 下载资料库" src="https://github.com/user-attachments/assets/1de14592-ebc6-4ee7-9b0c-17e7e0073171">
</p>

<br>

## 为 Mac 打造

Pastel 使用 SwiftUI 构建，采用 macOS 26 的 Liquid Glass 设计，并提供简体中文、繁体中文、日语、韩语和泰语界面。Pastel 会使用 Mac 的系统代理设置，也支持 HTTP、HTTPS、SOCKS5、`ALL_PROXY` 和 `NO_PROXY`。

<p align="center">
  <img width="820" alt="Pastel 多语言界面" src="https://github.com/user-attachments/assets/e6ef07a0-8834-457d-87f7-0bea14b45633">
</p>

<br>

## 开始使用

1. 下载最新的 [Pastel DMG](https://github.com/EEliberto/Pastel-macOS/releases/latest)。
2. 将 Pastel 拖入“应用程序”文件夹并打开。
3. 前往“设置”→“Apple 账户”，添加账户并按提示完成双重认证。
4. 选择 App 与版本，然后开始下载。

<p align="center">
  <img width="640" alt="Pastel Apple 账户设置" src="https://github.com/user-attachments/assets/c9efab09-2c9e-4593-908a-f01845b88465">
</p>

Apple 账户密码保存在 macOS 钥匙串中，会话数据经过加密后存储。登录和下载使用 macOS 的 StoreServices，需要在 Apple Silicon Mac 上运行；虚拟机不受支持。

<br>

## 20260929

此更新提高了历史版本下载的可靠性，修复了部分 App 下载指定版本时可能显示 “No Longer Available” 的问题，并改进了版本验证和错误提示。建议所有用户安装。

[阅读完整更新日志](release-notes/20260929/release-notes.md)

<br>

## 从源码构建

```bash
cd NodeProject
npm install
cd ..
open Pastel.xcodeproj
```

使用 Xcode 构建并运行 `Pastel` scheme。

<details>
  <summary>开源项目与致谢</summary>
  <br>
  Apple 登录与下载流程参考 <a href="https://github.com/majd/ipatool">majd/ipatool</a>、<a href="https://github.com/beer-psi/ipatool.ts">beer-psi/ipatool.ts</a>、<a href="https://github.com/SideStore/SideStore">SideStore</a> 与 <a href="https://github.com/Lakr233/Asspp">Lakr233/Asspp</a>。多语言翻译由 Claude 协助完成。
</details>

<br>

<div align="center">
  <p><a href="https://github.com/EEliberto/Pastel-macOS/issues">报告问题</a>&nbsp;&nbsp;·&nbsp;&nbsp;<a href="LICENSE">Apache License 2.0</a></p>
  <sub>Pastel 与 Apple Inc. 无隶属关系。App Store、Mac、iPhone、iPad 与 AirDrop 是 Apple Inc. 的商标。</sub>
</div>
