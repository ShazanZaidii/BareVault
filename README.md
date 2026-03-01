# 📦 BareVault

[![Go Version](https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go)](https://golang.org/)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](https://opensource.org/licenses/MIT)
[![Platform](https://img.shields.io/badge/Platform-Mac%20%7C%20Win%20%7C%20Linux-lightgrey)](#)

**Zero-Config, Cloud-Native Object Storage running on your own hardware.**

BareVault turns any local folder into a secure, globally accessible, S3-compatible object store in seconds. No port-forwarding, no AWS storage fees, and zero complex infrastructure configuration. Just run a single binary, and your local data is instantly available via a sleek, unified web portal.

[**🌐 Open the BareVault Portal**](https://shazanzaidii.github.io/BareVault/) • [**💬 Join the Discussions**](https://github.com/ShazanZaidii/BareVault/discussions)

---


<img width="1496" height="967" alt="image" src="https://github.com/user-attachments/assets/d7e551f5-9b6d-4f53-a568-ecf1ed7c22b1" />


<img width="1496" height="967" alt="image" src="https://github.com/user-attachments/assets/c452e4ff-853d-4c50-8238-41afbd7d7275" />

<img width="1496" height="967" alt="image" src="https://github.com/user-attachments/assets/b80aa349-cab6-4a8a-ada8-9cf7c917ca0f" />

<img width="1496" height="967" alt="image" src="https://github.com/user-attachments/assets/a2ba1b03-6628-46b8-bb1e-83b3de98c374" />




## ✨ Key Features

* **Global Edge Routing-**<br>
  Automatically negotiates secure Cloudflare Tunnels on startup. Access your local storage from anywhere in the world without exposing your IP or touching your router settings.

* **Granular IAM & Team Access:**<br>
  Generate secure, expiring invite links directly from the dashboard. Assign precise roles (`Admin`, `View/Upload/Delete`, `Read-Only`) and restrict users to specific buckets.

* **Universal Web Client:**<br>
  Manage all your vaults through a single, professionally designed AWS-style dashboard hosted on GitHub Pages. Features include bulk ZIP downloads, in-browser media previews, and drag-and-drop uploads.

* **Single Executable:**<br>
  No Docker, no dependencies, no messy config files. The entire engine (managing MinIO and Cloudflared) is packaged into one self-contained, obfuscated binary.

* **Smart Self-Healing Tunnels:**<br>
  Built-in connection management detects Cloudflare IP rate-limits and intelligently retries, ensuring your vault stays online.
---

## Quick Start

Getting your vault online takes less than 30 seconds.

### 1. Download the Engine
Grab the latest compiled binary for your operating system from the [Releases Tab](https://github.com/ShazanZaidii/BareVault/releases).

### 2. Ignite the Vault
Just double click the downloaded release and a terminal window should pop up. The Options are pretty straight forward to choose from. Also, PLEASE ENABLE YOUR CLIPBOARD if you haven't already because with it you will get direct credentials pasted for easier access.

### --> On Windows:
Press `Window Key + V` this will prompt you to enable clipboard if you haven't already.

### --> Mac:
Press `Cmd + Spacebar` to first open spotlight and then `Command + 4` to switch to clipboard.



