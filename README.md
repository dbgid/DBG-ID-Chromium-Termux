# DBG ID Chromium Termux

inspired or ported from [Seledroid](https://github.com/luanon404/seledroid/tree/main/seledroid).

This module provides a Termux/Pydroid-oriented Android Chromium WebDriver client for:

- launching `com.dbgid.browser`
- normal browser use
- WebDriver control when payload is provided to `SplashActivity`
- Turnstile / Cloudflare detection
- manual token wait flow
- realtime navigation wait

## Download APK

Download required APK: [DBG ID Browser](https://github.com/dbgid/DBG-ID-Browser)

## Installation
```bash
go get github.com/dbgid/DBG-ID-Chromium@latest
```
1. Install the DBG ID Browser APK on Android.
2. Put this module in your Go workspace.
3. Make sure `ip2asn-v4-u32.tsv` stays beside the module if you want generated IP headers from the ASN dataset.
4. Import the module as:

```go
import dbgidchromium "github.com/dbgid/DBG-ID-Chromium"
```

## Quick Start

```go
package main

import (
	"fmt"
	"log"

	dbgidchromium "dbgidchromium"
)

func main() {
	opts := dbgidchromium.DefaultOptions()
	opts.GUI = true

	driver, err := dbgidchromium.NewWebDriver(opts)
	if err != nil {
		log.Fatal(err)
	}
	stopInterrupt := dbgidchromium.HandleInterrupt(driver)
	defer stopInterrupt()
	defer func() {
		_, _ = driver.Quit()
	}()

	if _, err := driver.Goto("https://claimyshare.io"); err != nil {
		log.Fatal(err)
	}

	status, err := driver.DetectTurnstile()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(status.HasTurnstileWidget, status.HasTurnstileAPI, status.HasCloudflareChallenge)

	token, err := driver.TurnsTileToken(true)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(token)
}
```

## Main Usage

| Item | Type | Use |
| --- | --- | --- |
| `DefaultOptions()` | function | Returns default driver options |
| `NewWebDriver(opts)` | function | Starts browser and connects WebDriver |
| `(*WebDriver).Goto(url)` | method | Navigate with automatic headers, IP, and page wait |
| `(*WebDriver).Get(url)` | method | Lower-level navigation wrapper with preparation steps |
| `(*WebDriver).WaitForNavigation()` | method | Realtime wait after click/link navigation until page is fully loaded |
| `DetectTurnstile(driver)` | function | Detect Turnstile widget/API/Cloudflare challenge in realtime |
| `TurnsTileToken(driver, waitManual)` | function | Get Turnstile token, optionally wait until manual solve succeeds |
| `Evaluate(driver)` | function | Return current page outer HTML |
| `Delay(seconds)` | function | Animated terminal-style countdown delay |
| `HandleInterrupt(driver)` | function | Cleanup on interrupt and safe process kill |
| `(*WebDriver).Quit()` | method | Close driver and force-stop Android activity/package |

## Options Fields

| Field | Type | Description |
| --- | --- | --- |
| `GUI` | `bool` | Show browser UI or keep it headless-like in app flow |
| `PipMode` | `bool` | Pass pip/webdriver mode flag to app payload |
| `Lang` | `string` | Language value sent on init |
| `Debug` | `bool` | Enable debug flag in init payload |
| `AutoRandomUserAgent` | `bool` | Rotate Android mobile user-agent automatically |
| `EnableProgressLog` | `bool` | Show styled realtime progress logs |
| `LogWriter` | `io.Writer` | Output target for progress logs |
| `NavigationTimeout` | `time.Duration` | Timeout for page waits and challenge waits |
| `ReadyStatePoll` | `time.Duration` | Poll interval for realtime page checks |
| `AcceptTimeout` | `time.Duration` | WebDriver socket accept timeout on startup |
| `RecvTimeout` | `time.Duration` | Driver receive timeout |

## TurnstileStatus Fields

| Field | Type | Description |
| --- | --- | --- |
| `HasTurnstileWidget` | `bool` | Widget or iframe marker found |
| `HasTurnstileAPI` | `bool` | Turnstile API/script found |
| `HasCloudflareChallenge` | `bool` | Challenge page/frame markers found |
| `HasResponseInput` | `bool` | `cf-turnstile-response` input exists |
| `HasToken` | `bool` | Response input already has a value |
| `ResponseValue` | `string` | Current Turnstile token value |
| `SiteKey` | `string` | Detected Turnstile site key |
| `ReadyState` | `string` | Current `document.readyState` |
| `WidgetCount` | `int` | Number of widget markers detected |
| `ChallengeFrameCount` | `int` | Number of challenge frames detected |
| `PageURL` | `string` | Current page URL |
| `PageTitle` | `string` | Current page title |

## Browser Modes

- Normal browser: open DBG ID Browser normally without WebDriver payload.
- WebDriver browser: launch through `NewWebDriver(...)`, which sends the init payload compatible with the Python Seledroid flow.
- `SplashActivity` decides whether to continue as WebDriver mode or fall back to normal browser behavior based on payload presence.
