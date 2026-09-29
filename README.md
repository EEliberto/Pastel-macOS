<div align="center">
  <br>
  <img src="https://github.com/user-attachments/assets/f302f5d2-730c-44d2-a2c1-63cdd75804f2" width="136" alt="Pastel 图标">
  <h1>Pastel</h1>
  <h3>每一个版本，都触手可及。</h3>
  <p>在 Mac 上搜索 App、找到历史版本，并将 IPA 轻松发送到 iPhone 或 iPad。</p>
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

## 想要的版本。现在就找到。

Pastel 将搜索、版本记录和下载放在同一个原生 Mac App 中。选择地区，搜索 App，然后从时间线中挑选需要的版本。Pastel 会自动为所选账户匹配对应的 App Store。

<p align="center">
  <img width="820" alt="Pastel App 版本详情" src="https://github.com/user-attachments/assets/f3685fee-445f-41bc-8fea-2d1e602dec92">
</p>

<br>

## 多个来源。一处呈现。

Pastel 汇集 Timbrd、Agzy、Bilin 与 Apple 提供的版本信息。来源只负责查找版本；IPA 始终从 Apple 获取。你也可以直接输入 App ID 与版本 ID，快速前往目标版本。

如果账户尚未拥有某个免费 App，Pastel 会在你确认后先获取下载许可，再继续下载。付费 App 需要由该账户事先购买。

<p align="center">
  <img width="560" alt="Pastel 版本来源选择" src="https://github.com/user-attachments/assets/4de67361-8727-4705-8718-f9be81bc7b01">
</p>

<br>

## 下载完成。随手发送。

所有 IPA 都集中显示在下载资料库中，版本、地区和账户信息一目了然。通过系统分享菜单或 AirDrop，即可发送到 iPhone 或 iPad。

<p align="center">
  <img width="900" alt="Pastel 下载资料库" src="https://github.com/user-attachments/assets/1de14592-ebc6-4ee7-9b0c-17e7e0073171">
</p>

<br>

## 为 Mac 而设计。

Pastel 使用 SwiftUI 构建，适配 macOS 26 的 Liquid Glass，并提供简体中文、繁体中文、日语、韩语和泰语界面。网络请求遵循 macOS 的系统代理设置，也支持 HTTP、HTTPS、SOCKS5、`ALL_PROXY` 与 `NO_PROXY`。

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

Apple 账户密码保存在 macOS 钥匙串中，会话数据经过加密后存储。登录和下载依赖 Apple 的 StoreServices，需要在真实的 Apple Silicon Mac 上运行；虚拟机不受支持。

<br>

## 20260929

这一版本修复了部分 App 下载指定历史版本时出现 “No Longer Available” 的问题，并加入严格的 App 与版本校验。已通过 Alipay 12.12.30、12.12.26 和 12.12.20 的完整下载测试。

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
