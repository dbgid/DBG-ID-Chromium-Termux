package dbgidchromium

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"math/rand/v2"
	"net"
	"net/netip"
	"net/textproto"
	"os"
	"os/exec"
	"os/signal"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const Version = "2.0.4"

const LatestStableChromeVersion = "145.0.7632.159"

const (
	SeledroidPackage  = "com.dbgid.browser"
	SeledroidActivity = "com.dbgid.browser/.SplashActivity"
	defaultIP2ASNFile = "ip2asn-v4-u32.tsv"
	startupAcceptStep = 8 * time.Second
	startupRetryDelay = 1500 * time.Millisecond
	startupMaxLaunch  = 4
)

type Command string

const (
	CommandClose               Command = "close"
	CommandClickJava           Command = "click java"
	CommandClearCookie         Command = "clear cookie"
	CommandClearCookies        Command = "clear cookies"
	CommandCurrentURL          Command = "current url"
	CommandClearLocalStorage   Command = "clear local storage"
	CommandClearSessionStorage Command = "clear session storage"
	CommandDeleteAllCookie     Command = "delete all cookie"
	CommandExecuteScript       Command = "execute script"
	CommandFindElement         Command = "find element"
	CommandFindElements        Command = "find elements"
	CommandGet                 Command = "get"
	CommandGetAttribute        Command = "get attribute"
	CommandGetCookie           Command = "get cookie"
	CommandGetCookies          Command = "get cookies"
	CommandGetUserAgent        Command = "get user agent"
	CommandGetHeaders          Command = "get headers"
	CommandGetLocalStorage     Command = "get local storage"
	CommandGetSessionStorage   Command = "get session storage"
	CommandGetRecaptchaV3Token Command = "get recaptcha v3 token"
	CommandInit                Command = "init"
	CommandOverrideJSFunction  Command = "override js function"
	CommandPageSource          Command = "page source"
	CommandRemoveAttribute     Command = "remove attribute"
	CommandSendKey             Command = "send key"
	CommandSendText            Command = "send text"
	CommandSetAttribute        Command = "set attribute"
	CommandSwipe               Command = "swipe"
	CommandSwipeDown           Command = "swipe down"
	CommandSwipeUp             Command = "swipe up"
	CommandSetCookie           Command = "set cookie"
	CommandSetUserAgent        Command = "set user agent"
	CommandSetProxy            Command = "set proxy"
	CommandScrollTo            Command = "scroll to"
	CommandSetHeaders          Command = "set headers"
	CommandSetLocalStorage     Command = "set local storage"
	CommandSetSessionStorage   Command = "set session storage"
	CommandTitle               Command = "title"
	CommandWaitUntilElement    Command = "wait until element"
	CommandWaitUntilNotElement Command = "wait until not element"
)

const (
	ByID              = "id"
	ByXPath           = "xpath"
	ByLinkText        = "link text"
	ByPartialLinkText = "partial link text"
	ByName            = "name"
	ByTagName         = "tag name"
	ByClassName       = "class name"
	ByCSSSelector     = "css selector"
)

const (
	KeyEnter = 66
	KeyTab   = 61
)

var (
	ErrApplicationClosed    = errors.New("application closed")
	ErrNoSuchElement        = errors.New("no such element")
	ErrInvalidElementState  = errors.New("invalid element state")
	ErrUnexpectedTagName    = errors.New("unexpected tag name")
	ErrUnsupportedPlatform  = errors.New("only supports termux and pydroid3 at this moment")
	ErrUnexpectedCommand    = errors.New("unexpected command in webdriver response")
	ErrWebDriverChannelBusy = errors.New("webdriver channel busy")
)

type Options struct {
	GUI                 bool
	PipMode             bool
	Lang                string
	Debug               bool
	AutoRandomUserAgent bool
	EnableProgressLog   bool
	LogWriter           io.Writer
	NavigationTimeout   time.Duration
	ReadyStatePoll      time.Duration
	AcceptTimeout       time.Duration
	RecvTimeout         time.Duration
}

func DefaultOptions() Options {
	return Options{
		GUI:                 true,
		PipMode:             false,
		Lang:                "en",
		Debug:               false,
		AutoRandomUserAgent: true,
		EnableProgressLog:   true,
		LogWriter:           os.Stdout,
		NavigationTimeout:   45 * time.Second,
		ReadyStatePoll:      250 * time.Millisecond,
		AcceptTimeout:       60 * time.Second,
		RecvTimeout:         60 * time.Minute,
	}
}

type WebDriver struct {
	listener    *net.TCPListener
	conn        *net.TCPConn
	host        string
	port        int
	maxRecv     int
	recvTimeout time.Duration
	execSem     chan struct{}

	autoRandomUserAgent bool
	enableProgressLog   bool
	logWriter           io.Writer
	navigationTimeout   time.Duration
	readyStatePoll      time.Duration
	logMu               sync.Mutex
	navMu               sync.Mutex
	lastNavigation      navigationSnapshot
	hasLastNavigation   bool

	timeoutMu       sync.Mutex
	overrideActive  bool
	overrideTimeout time.Duration

	ShutUp bool
}

type Locator struct {
	By    string
	Value string
}

type Condition func(driver *WebDriver, command Command) (any, error)

type WebDriverWait struct {
	driver  *WebDriver
	timeout time.Duration
}

type WebElement struct {
	driver *WebDriver
	data   map[string]any
}

type Select struct {
	element    *WebElement
	isMultiple bool
}

type TurnstileStatus struct {
	HasTurnstileWidget     bool
	HasTurnstileAPI        bool
	HasCloudflareChallenge bool
	HasResponseInput       bool
	HasToken               bool
	ResponseValue          string
	SiteKey                string
	ReadyState             string
	WidgetCount            int
	ChallengeFrameCount    int
	PageURL                string
	PageTitle              string
}

type navigationSnapshot struct {
	URL        string
	Title      string
	ReadyState string
	HTMLSize   int
}

type ip2asnDB struct {
	starts []uint32
	ends   []uint32
	ccs    []string
	names  []string
}

var modernChromeMobileUserAgents = []string{
	fmt.Sprintf("Mozilla/5.0 (Linux; Android 15; Xiaomi 15) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%s Mobile Safari/537.36", LatestStableChromeVersion),
	fmt.Sprintf("Mozilla/5.0 (Linux; Android 15; POCO F7 Ultra) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%s Mobile Safari/537.36", LatestStableChromeVersion),
	fmt.Sprintf("Mozilla/5.0 (Linux; Android 15; Samsung Galaxy S25 Ultra) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%s Mobile Safari/537.36", LatestStableChromeVersion),
	fmt.Sprintf("Mozilla/5.0 (Linux; Android 15; vivo X200 Pro) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%s Mobile Safari/537.36", LatestStableChromeVersion),
	fmt.Sprintf("Mozilla/5.0 (Linux; Android 15; OPPO Find X9 Pro) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%s Mobile Safari/537.36", LatestStableChromeVersion),
	fmt.Sprintf("Mozilla/5.0 (Linux; Android 15; HUAWEI Mate X6) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%s Mobile Safari/537.36", LatestStableChromeVersion),
}

var (
	chromeVersionPattern = regexp.MustCompile(`Chrome/([0-9.]+)`)
	androidUAPattern     = regexp.MustCompile(`Android ([0-9]+(?:[._][0-9]+)*);\s*([^)]+?)\)`)
	ip2asnLoadOnce       sync.Once
	ip2asnData           *ip2asnDB
	ip2asnLoadErr        error
)

func New() (*WebDriver, error) {
	return NewWebDriver(DefaultOptions())
}

func NewWebDriver(opts Options) (*WebDriver, error) {
	if opts.AcceptTimeout <= 0 {
		opts.AcceptTimeout = 60 * time.Second
	}
	if opts.RecvTimeout <= 0 {
		opts.RecvTimeout = 60 * time.Minute
	}
	if opts.Lang == "" {
		opts.Lang = "en"
	}
	if opts.NavigationTimeout <= 0 {
		opts.NavigationTimeout = 45 * time.Second
	}
	if opts.ReadyStatePoll <= 0 {
		opts.ReadyStatePoll = 250 * time.Millisecond
	}
	if opts.LogWriter == nil {
		opts.LogWriter = os.Stdout
	}

	listener, err := net.ListenTCP("tcp", &net.TCPAddr{
		IP:   net.ParseIP("127.0.0.1"),
		Port: 0,
	})
	if err != nil {
		return nil, err
	}

	addr := listener.Addr().(*net.TCPAddr)
	driver := &WebDriver{
		listener:            listener,
		host:                "127.0.0.1",
		port:                addr.Port,
		maxRecv:             4096,
		recvTimeout:         opts.RecvTimeout,
		execSem:             make(chan struct{}, 1),
		autoRandomUserAgent: opts.AutoRandomUserAgent,
		enableProgressLog:   opts.EnableProgressLog,
		logWriter:           opts.LogWriter,
		navigationTimeout:   opts.NavigationTimeout,
		readyStatePoll:      opts.ReadyStatePoll,
	}

	initPayload := map[string]any{
		"command":  string(CommandInit),
		"pip_mode": opts.PipMode,
		"lang":     opts.Lang,
		"debug":    opts.Debug,
		"host":     driver.host,
		"port":     driver.port,
		"state":    opts.GUI,
	}

	if err := driver.startAndroidSession(initPayload); err != nil {
		_ = listener.Close()
		return nil, err
	}

	conn, err := driver.acceptDriverConnection(initPayload, opts.AcceptTimeout)
	if err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("could not connect chrome webdriver: %w", err)
	}
	driver.conn = conn

	if _, err := driver.rawGet("about:blank"); err != nil {
		_ = driver.closeLocal()
		return nil, err
	}
	_ = driver.captureAndStoreNavigationSnapshot()
	return driver, nil
}

func (d *WebDriver) Host() string {
	return d.host
}

func (d *WebDriver) Port() int {
	return d.port
}

func (d *WebDriver) startAndroidSession(initPayload map[string]any) error {
	home := os.Getenv("HOME")
	switch {
	case strings.Contains(home, "pydroid3"):
		payloadJSON, err := json.Marshal(initPayload)
		if err != nil {
			return err
		}
		rpcPath := os.Getenv("PYDROID_RPC")
		if rpcPath == "" {
			return errors.New("PYDROID_RPC is not set")
		}
		wrapped := map[string]any{
			"method": "launch-intent",
			"action": "android.intent.action.VIEW",
			"data":   "webdriver://com.dbgid.browser/?data=" + base64.StdEncoding.EncodeToString(payloadJSON),
		}
		conn, err := net.Dial("unix", rpcPath)
		if err != nil {
			return err
		}
		defer conn.Close()
		line, err := json.Marshal(wrapped)
		if err != nil {
			return err
		}
		_, err = conn.Write(append(line, '\n'))
		return err
	case strings.Contains(home, "termux"):
		data := pythonLiteral(initPayload) + "\n"
		return d.launchAndroidActivity(data)
	default:
		return ErrUnsupportedPlatform
	}
}

func LaunchCommand(action, name, data string) string {
	return fmt.Sprintf("am %s -n %s -d '%s' > /dev/null", action, name, data)
}

func ForceStopCommand(target string) string {
	return fmt.Sprintf("am force-stop %s > /dev/null", normalizeAndroidTargetPackage(target))
}

func (d *WebDriver) acceptDriverConnection(initPayload map[string]any, timeout time.Duration) (*net.TCPConn, error) {
	deadline := time.Now().Add(timeout)
	launchCount := 1
	lastErr := error(nil)

	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			if lastErr != nil {
				return nil, lastErr
			}
			return nil, errors.New("time out waiting for webdriver connection")
		}

		step := minDuration(startupAcceptStep, remaining)
		if err := d.listener.SetDeadline(time.Now().Add(step)); err != nil {
			return nil, err
		}
		conn, err := d.listener.AcceptTCP()
		if err == nil {
			_ = d.listener.SetDeadline(time.Time{})
			return conn, nil
		}
		lastErr = err

		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			if launchCount < startupMaxLaunch {
				d.logStartup("launch timeout, retrying webdriver start")
				time.Sleep(startupRetryDelay)
				if relaunchErr := d.startAndroidSession(initPayload); relaunchErr != nil {
					lastErr = errors.Join(err, relaunchErr)
				}
				launchCount++
				continue
			}
			continue
		}
		return nil, err
	}
}

func (d *WebDriver) launchAndroidActivity(data string) error {
	attempts := []struct {
		args []string
		desc string
	}{
		{
			args: []string{"start", "-W", "-S", "-n", SeledroidActivity, "-d", data},
			desc: "am start -W -S",
		},
		{
			args: []string{"start", "-W", "-n", SeledroidActivity, "-d", data},
			desc: "am start -W",
		},
		{
			args: []string{"start", "-n", SeledroidActivity, "-d", data},
			desc: "am start",
		},
	}

	var lastErr error
	for index, attempt := range attempts {
		output, err := runAMCommand(attempt.args...)
		if err == nil {
			if text := strings.TrimSpace(string(output)); text != "" && strings.Contains(strings.ToLower(text), "error") {
				lastErr = fmt.Errorf("%s: %s", attempt.desc, text)
			} else {
				return nil
			}
		} else {
			lastErr = fmt.Errorf("%s: %w", attempt.desc, err)
			if text := strings.TrimSpace(string(output)); text != "" {
				lastErr = fmt.Errorf("%w: %s", lastErr, text)
			}
		}

		if index < len(attempts)-1 {
			d.logStartup("launch failed, retrying activity start")
			time.Sleep(startupRetryDelay)
		}
	}
	return lastErr
}

func runAMCommand(args ...string) ([]byte, error) {
	cmd := exec.Command("am", args...)
	return cmd.CombinedOutput()
}

func (d *WebDriver) executeTimeout() time.Duration {
	d.timeoutMu.Lock()
	defer d.timeoutMu.Unlock()
	if d.overrideActive {
		return d.overrideTimeout
	}
	return d.recvTimeout
}

func (d *WebDriver) withTimeout(timeout time.Duration, fn func() (any, error)) (any, error) {
	d.timeoutMu.Lock()
	prevActive := d.overrideActive
	prevTimeout := d.overrideTimeout
	d.overrideActive = true
	d.overrideTimeout = timeout
	d.timeoutMu.Unlock()

	defer func() {
		d.timeoutMu.Lock()
		d.overrideActive = prevActive
		d.overrideTimeout = prevTimeout
		d.timeoutMu.Unlock()
	}()

	return fn()
}

func (d *WebDriver) Execute(command Command, kwargs map[string]any) (map[string]any, error) {
	select {
	case d.execSem <- struct{}{}:
	case <-time.After(200 * time.Millisecond):
		return nil, ErrWebDriverChannelBusy
	}
	defer func() {
		<-d.execSem
	}()

	if d.conn == nil {
		return nil, fmt.Errorf("%w: no active connection", ErrApplicationClosed)
	}

	request := map[string]any{
		"command": string(command),
	}
	for key, value := range kwargs {
		request[key] = value
	}

	payload := append(encodeRequestMap(request), '\n')
	timeout := d.executeTimeout()
	if timeout > 0 {
		_ = d.conn.SetDeadline(time.Now().Add(timeout))
	} else {
		_ = d.conn.SetDeadline(time.Time{})
	}

	if err := writeAll(d.conn, payload); err != nil {
		return nil, err
	}

	raw, err := d.recvAll()
	if err != nil {
		return nil, err
	}
	response, err := decodeResponse(raw)
	if err != nil {
		return nil, err
	}
	if responseCommand(response) != string(command) {
		return nil, ErrUnexpectedCommand
	}
	return response, nil
}

func (d *WebDriver) recvAll() (string, error) {
	var result bytes.Buffer
	var lengthBytes bytes.Buffer
	headerDone := false

	for !headerDone {
		buf := make([]byte, 1)
		n, err := d.conn.Read(buf)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return "", fmt.Errorf("%w: connection closed while waiting response header", ErrApplicationClosed)
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				return "", errors.New("time out to receive data")
			}
			return "", err
		}
		if n == 0 {
			return "", fmt.Errorf("%w: connection closed while waiting response header", ErrApplicationClosed)
		}
		b := buf[0]
		if b >= '0' && b <= '9' {
			lengthBytes.WriteByte(b)
			continue
		}
		result.WriteByte(b)
		headerDone = true
	}

	length, err := strconv.Atoi(lengthBytes.String())
	if err != nil {
		return "", fmt.Errorf("%w: please close me by driver.Close()", ErrApplicationClosed)
	}

	for result.Len() < length {
		remaining := length - result.Len()
		chunk := make([]byte, minInt(d.maxRecv, remaining))
		n, err := d.conn.Read(chunk)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return "", fmt.Errorf("%w: connection closed while receiving response payload", ErrApplicationClosed)
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				return "", errors.New("time out to receive data")
			}
			return "", err
		}
		if n == 0 {
			return "", fmt.Errorf("%w: connection closed while receiving response payload", ErrApplicationClosed)
		}
		result.Write(chunk[:n])
	}

	return html.UnescapeString(string(result.Bytes()[:length])), nil
}

func (d *WebDriver) closeLocal() error {
	var errs []error
	if d.conn != nil {
		if err := d.conn.Close(); err != nil {
			errs = append(errs, err)
		}
		d.conn = nil
	}
	if d.listener != nil {
		if err := d.listener.Close(); err != nil {
			errs = append(errs, err)
		}
		d.listener = nil
	}
	return errors.Join(errs...)
}

func (d *WebDriver) Close() (any, error) {
	response, err := d.Execute(CommandClose, nil)
	localErr := d.closeLocal()
	if err != nil {
		return nil, errors.Join(err, localErr)
	}
	return response["result"], localErr
}

func (d *WebDriver) Quit() (any, error) {
	closeResult, closeErr := d.closeForQuit()
	stopActivityErr := forceStopTarget(SeledroidActivity)
	stopPackageErr := forceStopTarget(SeledroidPackage)
	if closeErr != nil || stopActivityErr != nil || stopPackageErr != nil {
		return closeResult, errors.Join(closeErr, stopActivityErr, stopPackageErr)
	}
	return closeResult, nil
}

func (d *WebDriver) closeForQuit() (any, error) {
	if d.conn == nil && d.listener == nil {
		return nil, nil
	}
	if d.conn == nil {
		return nil, d.closeLocal()
	}
	return d.Close()
}

func (d *WebDriver) CurrentURL() (string, error) {
	return d.resultString(CommandCurrentURL, nil)
}

func (d *WebDriver) ClearCookie(cookieName, url string) (any, error) {
	return d.result(CommandClearCookie, map[string]any{
		"url":         url,
		"cookie_name": cookieName,
	})
}

func (d *WebDriver) ClearCookies(url string) (any, error) {
	return d.result(CommandClearCookies, map[string]any{"url": url})
}

func (d *WebDriver) ClickJava(x, y float64) (any, error) {
	return d.result(CommandClickJava, map[string]any{
		"position": fmt.Sprintf("%f %f", x, y),
	})
}

func (d *WebDriver) ClearLocalStorage() (any, error) {
	return d.result(CommandClearLocalStorage, nil)
}

func (d *WebDriver) ClearSessionStorage() (any, error) {
	return d.result(CommandClearSessionStorage, nil)
}

func (d *WebDriver) DeleteAllCookie() (any, error) {
	return d.result(CommandDeleteAllCookie, nil)
}

func (d *WebDriver) ExecuteScript(script string) (any, error) {
	currentURL, err := d.CurrentURL()
	if err != nil {
		return nil, err
	}
	if currentURL == "" {
		return nil, nil
	}
	return d.result(CommandExecuteScript, map[string]any{"script": script})
}

func (d *WebDriver) FindElementByID(id string) (*WebElement, error) {
	return d.FindElement(ByID, id, "")
}

func (d *WebDriver) FindElementByXPath(xpath string) (*WebElement, error) {
	return d.FindElement(ByXPath, xpath, "")
}

func (d *WebDriver) FindElementByLinkText(linkText string) (*WebElement, error) {
	return d.FindElement(ByLinkText, linkText, "")
}

func (d *WebDriver) FindElementByPartialLinkText(partialLinkText string) (*WebElement, error) {
	return d.FindElement(ByPartialLinkText, partialLinkText, "")
}

func (d *WebDriver) FindElementByName(name string) (*WebElement, error) {
	return d.FindElement(ByName, name, "")
}

func (d *WebDriver) FindElementByTagName(tagName string) (*WebElement, error) {
	return d.FindElement(ByTagName, tagName, "")
}

func (d *WebDriver) FindElementByClassName(className string) (*WebElement, error) {
	return d.FindElement(ByClassName, className, "")
}

func (d *WebDriver) FindElementByCSSSelector(selector string) (*WebElement, error) {
	return d.FindElement(ByCSSSelector, selector, "")
}

func (d *WebDriver) FindElementsByID(id string) ([]*WebElement, error) {
	return d.FindElements(ByID, id)
}

func (d *WebDriver) FindElementsByXPath(xpath string) ([]*WebElement, error) {
	return d.FindElements(ByXPath, xpath)
}

func (d *WebDriver) FindElementsByLinkText(linkText string) ([]*WebElement, error) {
	return d.FindElements(ByLinkText, linkText)
}

func (d *WebDriver) FindElementsByPartialLinkText(partialLinkText string) ([]*WebElement, error) {
	return d.FindElements(ByPartialLinkText, partialLinkText)
}

func (d *WebDriver) FindElementsByName(name string) ([]*WebElement, error) {
	return d.FindElements(ByName, name)
}

func (d *WebDriver) FindElementsByTagName(tagName string) ([]*WebElement, error) {
	return d.FindElements(ByTagName, tagName)
}

func (d *WebDriver) FindElementsByClassName(className string) ([]*WebElement, error) {
	return d.FindElements(ByClassName, className)
}

func (d *WebDriver) FindElementsByCSSSelector(selector string) ([]*WebElement, error) {
	return d.FindElements(ByCSSSelector, selector)
}

func (d *WebDriver) FindElement(by, value string, command Command) (*WebElement, error) {
	request := map[string]any{
		"by":    by,
		"value": value,
	}
	if command != "" {
		request["request"] = string(command)
	}
	response, err := d.Execute(CommandFindElement, request)
	if err != nil {
		return nil, err
	}
	if !truthy(response["result"]) && !d.ShutUp {
		return nil, fmt.Errorf("%w: no element match with by=By.%s value=%s", ErrNoSuchElement, by, value)
	}
	return &WebElement{driver: d, data: response}, nil
}

func (d *WebDriver) FindElements(by, value string) ([]*WebElement, error) {
	response, err := d.Execute(CommandFindElements, map[string]any{
		"by":    by,
		"value": value,
	})
	if err != nil {
		return nil, err
	}

	items, ok := asSlice(response["result"])
	if !ok {
		return nil, nil
	}

	path := asString(response["path"])
	result := make([]*WebElement, 0, len(items))
	for _, item := range items {
		pair, ok := asSlice(item)
		if !ok || len(pair) < 2 {
			continue
		}
		data := copyMap(response)
		data["command"] = string(CommandFindElement)
		data["path"] = fmt.Sprintf("%s[%v]", path, pair[0])
		data["result"] = pair[1]
		result = append(result, &WebElement{driver: d, data: data})
	}
	return result, nil
}

func (d *WebDriver) Get(url string) (any, error) {
	activeUserAgent := ""
	activeIP := ""
	activeIPCC := ""
	activeIPASN := ""
	if d.autoRandomUserAgent {
		if err := d.runProgressStep("rotating user agent", func() error {
			var err error
			activeUserAgent, err = d.SetRandomModernChromeUserAgent()
			return err
		}); err != nil {
			return nil, err
		}
	}

	if err := d.runProgressStep("syncing android headers", func() error {
		if activeUserAgent == "" {
			currentUserAgent, err := d.UserAgent()
			if err != nil {
				return err
			}
			activeUserAgent = currentUserAgent
		}
		var err error
		activeIP, activeIPCC, activeIPASN, err = d.SetAndroidClientHints(activeUserAgent)
		return err
	}); err != nil {
		return nil, err
	}
	d.logIPUsage(activeIP, activeIPCC, activeIPASN)

	if err := d.runProgressStep("clearing browsing state", d.ClearBrowsingState); err != nil {
		return nil, err
	}

	var result any
	if err := d.runProgressStep("navigating "+url, func() error {
		var err error
		result, err = d.rawGet(url)
		return err
	}); err != nil {
		return nil, err
	}

	if err := d.runProgressStep("waiting for dom ready", func() error {
		_, err := d.WaitForDOMContentLoaded(d.navigationTimeout)
		return err
	}); err != nil {
		return nil, err
	}

	_ = d.captureAndStoreNavigationSnapshot()

	return result, nil
}

func (d *WebDriver) Goto(url string) (*WebDriver, error) {
	if _, err := d.Get(url); err != nil {
		return nil, err
	}
	currentURL, err := d.CurrentURL()
	if err != nil {
		return nil, err
	}
	title, err := d.Title()
	if err != nil {
		return nil, err
	}
	userAgent, err := d.UserAgent()
	if err != nil {
		return nil, err
	}
	d.logSnapshot(currentURL, title, userAgent)
	return d, nil
}

func (d *WebDriver) rawGet(url string) (any, error) {
	return d.result(CommandGet, map[string]any{"url": url})
}

func (d *WebDriver) ClearBrowsingState() error {
	if _, err := d.DeleteAllCookie(); err != nil {
		return err
	}
	if _, err := d.ClearLocalStorage(); err != nil {
		return err
	}
	if _, err := d.ClearSessionStorage(); err != nil {
		return err
	}
	if err := d.ClearCache(); err != nil {
		return err
	}
	return nil
}

func (d *WebDriver) ClearCache() error {
	_, err := d.ExecuteScript(`(() => {
		try {
			if (typeof caches !== "undefined" && caches.keys) {
				caches.keys().then(keys => {
					for (const key of keys) {
						caches.delete(key);
					}
				}).catch(() => {});
			}
		} catch (e) {}
		return true;
	})()`)
	return err
}

func (d *WebDriver) WaitForDOMContentLoaded(timeout time.Duration) (string, error) {
	return d.waitForReadyState(timeout, false)
}

func (d *WebDriver) WaitForPageLoaded(timeout time.Duration) (string, error) {
	return d.waitForReadyState(timeout, true)
}

func (d *WebDriver) GetCookie(cookieName, url string) (any, error) {
	return d.result(CommandGetCookie, map[string]any{
		"url":         url,
		"cookie_name": cookieName,
	})
}

func (d *WebDriver) GetCookies(url string) (any, error) {
	return d.result(CommandGetCookies, map[string]any{"url": url})
}

func (d *WebDriver) GetLocalStorage() (map[string]any, error) {
	response, err := d.Execute(CommandGetLocalStorage, nil)
	if err != nil {
		return nil, err
	}
	if result, ok := asMap(response["result"]); ok {
		return result, nil
	}
	return nil, fmt.Errorf("unexpected local storage payload: %T", response["result"])
}

func (d *WebDriver) GetSessionStorage() (map[string]any, error) {
	response, err := d.Execute(CommandGetSessionStorage, nil)
	if err != nil {
		return nil, err
	}
	if result, ok := asMap(response["result"]); ok {
		return result, nil
	}
	return nil, fmt.Errorf("unexpected session storage payload: %T", response["result"])
}

func (d *WebDriver) GetRecaptchaV3Token(action string) (any, error) {
	element, err := d.FindElement(ByCSSSelector, `script[src*="https://www.google.com/recaptcha/api.js?render="]`, "")
	if err != nil {
		if errors.Is(err, ErrNoSuchElement) {
			return nil, nil
		}
		return nil, err
	}
	srcValue, err := element.GetAttribute("src")
	if err != nil {
		return nil, err
	}
	siteKey := strings.ReplaceAll(asString(srcValue), "https://www.google.com/recaptcha/api.js?render=", "")
	return d.result(CommandGetRecaptchaV3Token, map[string]any{
		"site_key": siteKey,
		"action":   action,
	})
}

func (d *WebDriver) Headers() (map[string]any, error) {
	response, err := d.Execute(CommandGetHeaders, nil)
	if err != nil {
		return nil, err
	}
	if result, ok := asMap(response["result"]); ok {
		return result, nil
	}
	return nil, fmt.Errorf("unexpected headers payload: %T", response["result"])
}

func (d *WebDriver) SetHeaders(headers map[string]string) error {
	normalized := make(map[string]string, len(headers))
	for key, value := range headers {
		normalized[textproto.CanonicalMIMEHeaderKey(key)] = value
	}
	body, err := json.Marshal(normalized)
	if err != nil {
		return err
	}
	_, err = d.result(CommandSetHeaders, map[string]any{"headers": string(body)})
	return err
}

func AndroidClientHintHeaders(userAgent string) (map[string]string, string, string, string) {
	fullVersion := extractChromeVersion(userAgent)
	majorVersion := extractChromeMajorVersion(fullVersion)
	androidVersion := extractAndroidVersion(userAgent)
	model := extractAndroidModel(userAgent)

	headers := map[string]string{
		"User-Agent":                  userAgent,
		"Sec-CH-UA":                   fmt.Sprintf(`"Chromium";v="%s", "Google Chrome";v="%s", "Not=A?Brand";v="24"`, majorVersion, majorVersion),
		"Sec-CH-UA-Mobile":            "?1",
		"Sec-CH-UA-Platform":          `"Android"`,
		"Sec-CH-UA-Platform-Version":  fmt.Sprintf(`"%s"`, androidVersion),
		"Sec-CH-UA-Model":             fmt.Sprintf(`"%s"`, model),
		"Sec-CH-UA-Full-Version":      fmt.Sprintf(`"%s"`, fullVersion),
		"Sec-CH-UA-Full-Version-List": fmt.Sprintf(`"Chromium";v="%s", "Google Chrome";v="%s", "Not=A?Brand";v="24.0.0.0"`, fullVersion, fullVersion),
		"Sec-CH-UA-Arch":              `""`,
		"Sec-CH-UA-Bitness":           `""`,
		"Sec-CH-UA-WoW64":             "?0",
		"Sec-CH-UA-Form-Factors":      `"Mobile"`,
	}
	ip, cc, name := applyGeneratedIPHeaders(headers)
	return headers, ip, cc, name
}

func (d *WebDriver) SetAndroidClientHints(userAgent string) (string, string, string, error) {
	if strings.TrimSpace(userAgent) == "" {
		userAgent = RandomModernChromeUserAgent()
		if err := d.SetUserAgent(userAgent); err != nil {
			return "", "", "", err
		}
	}
	headers, ip, cc, name := AndroidClientHintHeaders(userAgent)
	if err := d.SetHeaders(headers); err != nil {
		return "", "", "", err
	}
	return ip, cc, name, nil
}

func (d *WebDriver) OverrideJSFunction(script string) (any, error) {
	return d.result(CommandOverrideJSFunction, map[string]any{"script": script})
}

func (d *WebDriver) PageSource() (string, error) {
	return d.resultString(CommandPageSource, nil)
}

func (d *WebDriver) Swipe(startX, startY, endX, endY float64, speed int) (any, error) {
	return d.result(CommandSwipe, map[string]any{
		"position": fmt.Sprintf("%f %f %f %f", startX, startY, endX, endY),
		"speed":    speed,
	})
}

func (d *WebDriver) SwipeDown() (any, error) {
	return d.result(CommandSwipeDown, nil)
}

func (d *WebDriver) SwipeUp() (any, error) {
	return d.result(CommandSwipeUp, nil)
}

func (d *WebDriver) SetCookie(cookieName string, value any, url string) (any, error) {
	return d.result(CommandSetCookie, map[string]any{
		"url":         url,
		"cookie_name": cookieName,
		"value":       value,
	})
}

func (d *WebDriver) SetProxy(host string, port int) (any, error) {
	return d.result(CommandSetProxy, map[string]any{
		"proxy": fmt.Sprintf("%s %d", host, port),
	})
}

func (d *WebDriver) ScrollTo(x, y int) (any, error) {
	return d.result(CommandScrollTo, map[string]any{
		"position": fmt.Sprintf("%d %d", x, y),
	})
}

func (d *WebDriver) SetLocalStorage(key string, value any, isString bool) (any, error) {
	return d.result(CommandSetLocalStorage, map[string]any{
		"key":       key,
		"value":     value,
		"is_string": strconv.FormatBool(isString),
	})
}

func (d *WebDriver) SetSessionStorage(key string, value any, isString bool) (any, error) {
	return d.result(CommandSetSessionStorage, map[string]any{
		"key":       key,
		"value":     value,
		"is_string": strconv.FormatBool(isString),
	})
}

func (d *WebDriver) UserAgent() (string, error) {
	return d.resultString(CommandGetUserAgent, nil)
}

func RandomModernChromeUserAgent() string {
	return randomChoice(modernChromeMobileUserAgents)
}

func RandomModernChromeMobileUserAgent() string {
	return randomChoice(modernChromeMobileUserAgents)
}

func (d *WebDriver) SetRandomModernChromeUserAgent() (string, error) {
	userAgent := RandomModernChromeUserAgent()
	if err := d.SetUserAgent(userAgent); err != nil {
		return "", err
	}
	return userAgent, nil
}

func (d *WebDriver) SetRandomModernChromeMobileUserAgent() (string, error) {
	userAgent := RandomModernChromeMobileUserAgent()
	if err := d.SetUserAgent(userAgent); err != nil {
		return "", err
	}
	return userAgent, nil
}

func (d *WebDriver) SetUserAgent(userAgent string) error {
	_, err := d.result(CommandSetUserAgent, map[string]any{"user_agent": userAgent})
	return err
}

func (d *WebDriver) Title() (string, error) {
	return d.resultString(CommandTitle, nil)
}

func Wait(delay time.Duration) {
	time.Sleep(delay)
}

func Delay(seconds int) {
	DefaultDelay(seconds)
}

func DefaultDelay(seconds int) {
	delayWithWriter(os.Stdout, seconds)
}

func Evaluate(driver *WebDriver) (string, error) {
	if driver == nil {
		return "", errors.New("driver is nil")
	}
	return driver.Evaluate()
}

func TurnsTileToken(driver *WebDriver, waitManual bool) (string, error) {
	if driver == nil {
		return "", errors.New("driver is nil")
	}
	return driver.TurnsTileToken(waitManual)
}

func DetectTurnstile(driver *WebDriver) (*TurnstileStatus, error) {
	if driver == nil {
		return nil, errors.New("driver is nil")
	}
	return driver.DetectTurnstile()
}

func WaitForTurnstile(driver *WebDriver, timeout time.Duration) (*TurnstileStatus, error) {
	if driver == nil {
		return nil, errors.New("driver is nil")
	}
	return driver.WaitForTurnstile(timeout)
}

func WaitForNavigation(driver *WebDriver) (*WebDriver, error) {
	if driver == nil {
		return nil, errors.New("driver is nil")
	}
	return driver.WaitForNavigation()
}

func HandleInterrupt(driver *WebDriver) func() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)

	var handled atomic.Bool
	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() {
			signal.Stop(signals)
			close(signals)
		})
	}

	go func() {
		sig, ok := <-signals
		if !ok || handled.Swap(true) {
			return
		}
		stop()

		if driver != nil {
			_, _ = driver.Quit()
		}

		killSignal := syscall.SIGTERM
		if unixSignal, ok := sig.(syscall.Signal); ok {
			killSignal = unixSignal
		}
		_ = SafeKillCurrentProcess(killSignal, 1500*time.Millisecond)
	}()

	return stop
}

func (d *WebDriver) Evaluate() (string, error) {
	result, err := d.ExecuteScript("document.documentElement ? document.documentElement.outerHTML : ''")
	if err != nil {
		return "", err
	}
	return asString(result), nil
}

func (d *WebDriver) TurnsTileToken(waitManual bool) (string, error) {
	if _, err := d.WaitForDOMContentLoaded(d.navigationTimeout); err != nil {
		return "", err
	}

	selector := `input[name='cf-turnstile-response']`
	if waitManual {
		if err := d.runProgressStep("waiting for manual turnstile solve", func() error {
			for {
				if _, err := d.WaitForDOMContentLoaded(d.navigationTimeout); err != nil {
					return err
				}

				element, err := d.FindElement(ByCSSSelector, selector, "")
				if err != nil {
					if errors.Is(err, ErrNoSuchElement) {
						time.Sleep(d.readyStatePoll)
						continue
					}
					return err
				}

				value, err := element.GetAttribute("value")
				if err != nil {
					return err
				}
				token := strings.TrimSpace(asString(value))
				if token != "" {
					return nil
				}

				token, err = d.turnsTileTokenValue()
				if err != nil {
					return err
				}
				if strings.TrimSpace(token) != "" {
					return nil
				}
				time.Sleep(d.readyStatePoll)
			}
		}); err != nil {
			return "", err
		}
	}

	return d.turnsTileTokenValue()
}

func (d *WebDriver) DetectTurnstile() (*TurnstileStatus, error) {
	result, err := d.ExecuteScript(`(() => {
		const queryAll = (selector) => Array.from(document.querySelectorAll(selector));
		const uniqueElements = (elements) => Array.from(new Set(elements.filter(Boolean)));
		const scripts = Array.from(document.scripts || []);
		const html = document.documentElement ? document.documentElement.outerHTML : "";
		const title = document.title || "";
		const url = location.href || "";
		const readyState = document.readyState || "";

		const responseInput = document.querySelector("input[name='cf-turnstile-response']");
		const responseValue = responseInput ? String(responseInput.value || "") : "";
		const hasResponseInput = !!responseInput;
		const hasToken = responseValue.trim() !== "";

		const widgetElements = uniqueElements([
			...queryAll(".cf-turnstile"),
			...queryAll("[data-sitekey][data-action]"),
			...queryAll("[data-sitekey][data-callback]"),
			...queryAll("[data-sitekey][data-theme]"),
			...queryAll("[data-turnstile-sitekey]"),
			...queryAll("iframe[src*='challenges.cloudflare.com/turnstile/']"),
			...queryAll("iframe[src*='challenges.cloudflare.com/cdn-cgi/challenge-platform/']"),
			...queryAll("iframe[title*='Cloudflare security challenge']"),
			...queryAll("iframe[title*='Widget containing a Cloudflare security challenge']"),
		]);
		const widgetCount = widgetElements.length;

		const challengeFrames = uniqueElements([
			...queryAll("iframe[src*='challenges.cloudflare.com']"),
			...queryAll("iframe[src*='cdn-cgi/challenge-platform']"),
			...queryAll("iframe[title*='Cloudflare']"),
		]);
		const challengeFrameCount = challengeFrames.length;

		const hasAPI =
			typeof window.turnstile === "object" ||
			typeof window.turnstile === "function" ||
			scripts.some((script) => /challenges\.cloudflare\.com\/turnstile\/|turnstile\/v0\/api\.js/i.test(script.src || ""));

		const siteKeyElement =
			document.querySelector(".cf-turnstile[data-sitekey]") ||
			document.querySelector("[data-turnstile-sitekey]") ||
			document.querySelector("[data-sitekey]");
		const siteKey = siteKeyElement
			? String(siteKeyElement.getAttribute("data-sitekey") || siteKeyElement.getAttribute("data-turnstile-sitekey") || "")
			: "";

		const challengeMarkers =
			!!window._cf_chl_opt ||
			!!document.querySelector("#cf-wrapper") ||
			!!document.querySelector("#challenge-running") ||
			!!document.querySelector(".challenge-running") ||
			!!document.querySelector(".cf-browser-verification") ||
			!!document.querySelector(".cf-challenge") ||
			!!document.querySelector("form[action*='/cdn-cgi/challenge-platform/']") ||
			!!document.querySelector("input[name*='cf_ch']") ||
			!!document.querySelector("script[data-cf-beacon]") ||
			/cdn-cgi\/challenge-platform/i.test(url) ||
			/challenges\.cloudflare\.com/i.test(url) ||
			/Just a moment/i.test(title) ||
			/Attention Required!/i.test(title) ||
			/cf-challenge|cf-browser-verification|challenge-running|managed-challenge|cf_chl_|turnstile/i.test(html);

		const hasWidget =
			widgetCount > 0 ||
			hasResponseInput ||
			hasToken ||
			(siteKey !== "" && (hasAPI || challengeFrameCount > 0));

		const hasCloudflareChallenge =
			challengeMarkers ||
			(challengeFrameCount > 0 && !hasToken) ||
			(hasWidget && /challenge|verify|checking/i.test(title));

		return {
			has_turnstile_widget: hasWidget,
			has_turnstile_api: hasAPI,
			has_cloudflare_challenge: hasCloudflareChallenge,
			has_response_input: hasResponseInput,
			has_token: hasToken,
			response_value: responseValue,
			site_key: siteKey,
			ready_state: readyState,
			widget_count: widgetCount,
			challenge_frame_count: challengeFrameCount,
			page_url: url,
			page_title: title
		};
	})()`)
	if err != nil {
		return nil, err
	}

	data, ok := asMap(result)
	if !ok {
		return nil, fmt.Errorf("unexpected turnstile detection payload: %T", result)
	}

	return &TurnstileStatus{
		HasTurnstileWidget:     truthy(data["has_turnstile_widget"]),
		HasTurnstileAPI:        truthy(data["has_turnstile_api"]),
		HasCloudflareChallenge: truthy(data["has_cloudflare_challenge"]),
		HasResponseInput:       truthy(data["has_response_input"]),
		HasToken:               truthy(data["has_token"]),
		ResponseValue:          asString(data["response_value"]),
		SiteKey:                asString(data["site_key"]),
		ReadyState:             asString(data["ready_state"]),
		WidgetCount:            asInt(data["widget_count"]),
		ChallengeFrameCount:    asInt(data["challenge_frame_count"]),
		PageURL:                asString(data["page_url"]),
		PageTitle:              asString(data["page_title"]),
	}, nil
}

func (d *WebDriver) WaitForTurnstile(timeout time.Duration) (*TurnstileStatus, error) {
	if timeout <= 0 {
		timeout = d.navigationTimeout
	}

	if err := d.runProgressStep("waiting for turnstile or cloudflare challenge", func() error {
		deadline := time.Now().Add(timeout)
		for {
			status, err := d.DetectTurnstile()
			if err == nil && status != nil && (status.HasTurnstileWidget || status.HasTurnstileAPI || status.HasCloudflareChallenge || status.HasResponseInput) {
				return nil
			}
			if time.Now().After(deadline) {
				return errors.New("time out waiting for turnstile or cloudflare challenge")
			}
			time.Sleep(d.readyStatePoll)
		}
	}); err != nil {
		return nil, err
	}

	return d.DetectTurnstile()
}

func (d *WebDriver) WaitForNavigation() (*WebDriver, error) {
	baseline, ok := d.lastNavigationSnapshotValue()
	if !ok {
		snapshot, err := d.captureNavigationSnapshot()
		if err != nil {
			return nil, err
		}
		baseline = snapshot
		d.storeNavigationSnapshot(snapshot)
	}

	if err := d.runProgressStep("waiting for navigation", func() error {
		deadline := time.Now().Add(d.navigationTimeout)
		sawNavigation := false
		var lastSnapshot navigationSnapshot

		for {
			snapshot, err := d.captureNavigationSnapshot()
			if err == nil {
				lastSnapshot = snapshot

				if d.navigationChanged(baseline, snapshot) || d.navigationInProgress(snapshot) {
					sawNavigation = true
				}

				if sawNavigation && d.navigationLoaded(snapshot) {
					d.storeNavigationSnapshot(snapshot)
					return nil
				}
			}

			if time.Now().After(deadline) {
				if sawNavigation && d.navigationLoaded(lastSnapshot) {
					d.storeNavigationSnapshot(lastSnapshot)
					return nil
				}
				return errors.New("time out waiting for navigation to complete")
			}
			time.Sleep(d.readyStatePoll)
		}
	}); err != nil {
		return nil, err
	}

	return d, nil
}

func (d *WebDriver) turnsTileTokenValue() (string, error) {
	result, err := d.ExecuteScript(`(() => {
		const element = document.querySelector("input[name='cf-turnstile-response']");
		return element ? element.value : "";
	})()`)
	if err != nil {
		return "", err
	}
	return asString(result), nil
}

func (d *WebDriver) result(command Command, kwargs map[string]any) (any, error) {
	response, err := d.Execute(command, kwargs)
	if err != nil {
		return nil, err
	}
	return response["result"], nil
}

func (d *WebDriver) resultString(command Command, kwargs map[string]any) (string, error) {
	value, err := d.result(command, kwargs)
	if err != nil {
		return "", err
	}
	return asString(value), nil
}

func (d *WebDriver) NewWait(timeout time.Duration) *WebDriverWait {
	return &WebDriverWait{driver: d, timeout: timeout}
}

func NewWebDriverWait(driver *WebDriver, timeout time.Duration) *WebDriverWait {
	return &WebDriverWait{driver: driver, timeout: timeout}
}

func (w *WebDriverWait) Until(condition Condition) (any, error) {
	return w.driver.withTimeout(w.timeout, func() (any, error) {
		return condition(w.driver, CommandWaitUntilElement)
	})
}

func (w *WebDriverWait) UntilNot(condition Condition) error {
	_, err := w.driver.withTimeout(w.timeout, func() (any, error) {
		return condition(w.driver, CommandWaitUntilNotElement)
	})
	if err != nil {
		return err
	}
	return nil
}

func PresenceOfElementLocated(locator Locator) Condition {
	return func(driver *WebDriver, command Command) (any, error) {
		element, err := driver.FindElement(locator.By, locator.Value, command)
		if err != nil {
			if errors.Is(err, ErrNoSuchElement) {
				return false, nil
			}
			return nil, err
		}
		if element == nil || !truthy(element.data["result"]) {
			return false, nil
		}
		return element, nil
	}
}

func VisibilityOfElementLocated(locator Locator) Condition {
	return func(driver *WebDriver, command Command) (any, error) {
		element, err := driver.FindElement(locator.By, locator.Value, command)
		if err != nil {
			if errors.Is(err, ErrNoSuchElement) {
				return false, nil
			}
			return nil, err
		}
		visible, err := element.IsDisplayed()
		if err != nil {
			return nil, err
		}
		if truthy(element.data["result"]) && visible {
			return element, nil
		}
		return false, nil
	}
}

func InvisibilityOfElementLocated(locator Locator) Condition {
	return func(driver *WebDriver, command Command) (any, error) {
		element, err := driver.FindElement(locator.By, locator.Value, command)
		if err != nil {
			if errors.Is(err, ErrNoSuchElement) {
				return false, nil
			}
			return nil, err
		}
		visible, err := element.IsDisplayed()
		if err != nil {
			return nil, err
		}
		if truthy(element.data["result"]) && !visible {
			return element, nil
		}
		return false, nil
	}
}

func ElementToBeClickable(locator Locator) Condition {
	return func(driver *WebDriver, command Command) (any, error) {
		element, err := driver.FindElement(locator.By, locator.Value, command)
		if err != nil {
			if errors.Is(err, ErrNoSuchElement) {
				return false, nil
			}
			return nil, err
		}
		visible, err := element.IsDisplayed()
		if err != nil {
			return nil, err
		}
		disabled, err := element.Disabled()
		if err != nil {
			return nil, err
		}
		if truthy(element.data["result"]) && visible && !disabled {
			return element, nil
		}
		return false, nil
	}
}

func (e *WebElement) String() string {
	return fmt.Sprintf("WebElement(%v)", e.data["result"])
}

func (e *WebElement) Click() (any, error) {
	return e.GetAttribute("click()")
}

func (e *WebElement) ClickJava() (any, error) {
	x, y, err := e.Position()
	if err != nil {
		return nil, err
	}
	return e.driver.result(CommandClickJava, map[string]any{
		"position": fmt.Sprintf("%f %f", x, y),
	})
}

func (e *WebElement) Clear() (any, error) {
	return e.SetAttribute("value", "", true)
}

func (e *WebElement) Focus() error {
	_, err := e.GetAttribute("focus()")
	return err
}

func (e *WebElement) FindElementByID(id string) (*WebElement, error) {
	return e.FindElement(ByID, id, "")
}

func (e *WebElement) FindElementByName(name string) (*WebElement, error) {
	return e.FindElement(ByName, name, "")
}

func (e *WebElement) FindElementByXPath(xpath string) (*WebElement, error) {
	return e.FindElement(ByXPath, xpath, "")
}

func (e *WebElement) FindElementByLinkText(linkText string) (*WebElement, error) {
	return e.FindElement(ByLinkText, linkText, "")
}

func (e *WebElement) FindElementByPartialLinkText(partialLinkText string) (*WebElement, error) {
	return e.FindElement(ByPartialLinkText, partialLinkText, "")
}

func (e *WebElement) FindElementByTagName(tagName string) (*WebElement, error) {
	return e.FindElement(ByTagName, tagName, "")
}

func (e *WebElement) FindElementByClassName(className string) (*WebElement, error) {
	return e.FindElement(ByClassName, className, "")
}

func (e *WebElement) FindElementByCSSSelector(selector string) (*WebElement, error) {
	return e.FindElement(ByCSSSelector, selector, "")
}

func (e *WebElement) FindElementsByID(id string) ([]*WebElement, error) {
	return e.FindElements(ByID, id)
}

func (e *WebElement) FindElementsByName(name string) ([]*WebElement, error) {
	return e.FindElements(ByName, name)
}

func (e *WebElement) FindElementsByXPath(xpath string) ([]*WebElement, error) {
	return e.FindElements(ByXPath, xpath)
}

func (e *WebElement) FindElementsByLinkText(linkText string) ([]*WebElement, error) {
	return e.FindElements(ByLinkText, linkText)
}

func (e *WebElement) FindElementsByPartialLinkText(partialLinkText string) ([]*WebElement, error) {
	return e.FindElements(ByPartialLinkText, partialLinkText)
}

func (e *WebElement) FindElementsByTagName(tagName string) ([]*WebElement, error) {
	return e.FindElements(ByTagName, tagName)
}

func (e *WebElement) FindElementsByClassName(className string) ([]*WebElement, error) {
	return e.FindElements(ByClassName, className)
}

func (e *WebElement) FindElementsByCSSSelector(selector string) ([]*WebElement, error) {
	return e.FindElements(ByCSSSelector, selector)
}

func (e *WebElement) FindElement(by, value string, command Command) (*WebElement, error) {
	request := map[string]any{
		"path":  asString(e.data["path"]),
		"by":    by,
		"value": value,
	}
	if command != "" {
		request["request"] = string(command)
	}
	response, err := e.driver.Execute(CommandFindElement, request)
	if err != nil {
		return nil, err
	}
	if !truthy(response["result"]) {
		return nil, fmt.Errorf("%w: no element match with by=By.%s value=%s", ErrNoSuchElement, by, value)
	}
	return &WebElement{driver: e.driver, data: response}, nil
}

func (e *WebElement) FindElements(by, value string) ([]*WebElement, error) {
	response, err := e.driver.Execute(CommandFindElements, map[string]any{
		"path":  asString(e.data["path"]),
		"by":    by,
		"value": value,
	})
	if err != nil {
		return nil, err
	}

	items, ok := asSlice(response["result"])
	if !ok {
		return nil, nil
	}

	path := asString(response["path"])
	result := make([]*WebElement, 0, len(items))
	for _, item := range items {
		pair, ok := asSlice(item)
		if !ok || len(pair) < 2 {
			continue
		}
		data := copyMap(response)
		data["command"] = string(CommandFindElement)
		data["path"] = fmt.Sprintf("%s[%v]", path, pair[0])
		data["result"] = pair[1]
		result = append(result, &WebElement{driver: e.driver, data: data})
	}
	return result, nil
}

func (e *WebElement) GetAttribute(attributeName string) (any, error) {
	if attributeName == "class" {
		attributeName = "className"
	}
	return e.executeElement(map[string]any{
		"request":        string(CommandGetAttribute),
		"attribute_name": attributeName,
	})
}

func (e *WebElement) Height() (float64, error) {
	value, err := e.GetAttribute("getBoundingClientRect().height")
	if err != nil {
		return 0, err
	}
	return asFloat64(value), nil
}

func (e *WebElement) Width() (float64, error) {
	value, err := e.GetAttribute("getBoundingClientRect().width")
	if err != nil {
		return 0, err
	}
	return asFloat64(value), nil
}

func (e *WebElement) InnerHTML() (string, error) {
	value, err := e.GetAttribute("innerHTML")
	if err != nil {
		return "", err
	}
	return asString(value), nil
}

func (e *WebElement) SetInnerHTML(text string) (any, error) {
	return e.SetAttribute("innerHTML", text, true)
}

func (e *WebElement) OuterHTML() (string, error) {
	value, err := e.GetAttribute("outerHTML")
	if err != nil {
		return "", err
	}
	return asString(value), nil
}

func (e *WebElement) SetOuterHTML(text string) (any, error) {
	return e.SetAttribute("outerHTML", text, true)
}

func (e *WebElement) Position() (float64, float64, error) {
	xValue, err := e.GetAttribute("getBoundingClientRect().x")
	if err != nil {
		return 0, 0, err
	}
	yValue, err := e.GetAttribute("getBoundingClientRect().y")
	if err != nil {
		return 0, 0, err
	}
	return asFloat64(xValue), asFloat64(yValue), nil
}

func (e *WebElement) Disabled() (bool, error) {
	value, err := e.GetAttribute("disabled")
	if err != nil {
		return false, err
	}
	return truthy(value), nil
}

func (e *WebElement) SetDisabled(value bool) (any, error) {
	if value {
		return e.SetAttribute("disabled", "true", false)
	}
	return e.RemoveAttribute("disabled")
}

func (e *WebElement) IsDisplayed() (bool, error) {
	height, err := e.Height()
	if err != nil {
		return false, err
	}
	width, err := e.Width()
	if err != nil {
		return false, err
	}
	return height > 0 && width > 0, nil
}

func (e *WebElement) ReadOnly() (bool, error) {
	value, err := e.GetAttribute("readOnly")
	if err != nil {
		return false, err
	}
	return truthy(value), nil
}

func (e *WebElement) RemoveAttribute(attributeName string) (any, error) {
	return e.executeElement(map[string]any{
		"request":        string(CommandRemoveAttribute),
		"attribute_name": attributeName,
	})
}

func (e *WebElement) SendKey(key any) (any, error) {
	readOnly, err := e.ReadOnly()
	if err != nil {
		return nil, err
	}
	if readOnly {
		return nil, fmt.Errorf("%w: element is read-only: %v", ErrInvalidElementState, e.data["result"])
	}
	if err := e.Focus(); err != nil {
		return nil, err
	}
	return e.executeElement(map[string]any{
		"request": string(CommandSendKey),
		"key":     key,
	})
}

func (e *WebElement) SendText(text string) (any, error) {
	readOnly, err := e.ReadOnly()
	if err != nil {
		return nil, err
	}
	if readOnly {
		return nil, fmt.Errorf("%w: element is read-only: %v", ErrInvalidElementState, e.data["result"])
	}
	if err := e.Focus(); err != nil {
		return nil, err
	}
	return e.executeElement(map[string]any{
		"request": string(CommandSendText),
		"text":    text,
	})
}

func (e *WebElement) SetAttribute(attributeName string, attributeValue any, isString bool) (any, error) {
	return e.executeElement(map[string]any{
		"request":         string(CommandSetAttribute),
		"attribute_name":  attributeName,
		"attribute_value": attributeValue,
		"is_string":       strconv.FormatBool(isString),
	})
}

func (e *WebElement) Value() (any, error) {
	return e.GetAttribute("value")
}

func (e *WebElement) SetValue(value any) (any, error) {
	return e.SetAttribute("value", value, true)
}

func (e *WebElement) executeElement(kwargs map[string]any) (any, error) {
	request := map[string]any{
		"path":  asString(e.data["path"]),
		"by":    asString(e.data["by"]),
		"value": e.data["value"],
	}
	for key, value := range kwargs {
		request[key] = value
	}
	response, err := e.driver.Execute(Command(asString(e.data["command"])), request)
	if err != nil {
		return nil, err
	}
	return response["result"], nil
}

func NewSelect(element *WebElement) (*Select, error) {
	tagNameValue, err := element.GetAttribute("tagName")
	if err != nil {
		return nil, err
	}
	tagName := asString(tagNameValue)
	if tagName != "SELECT" {
		return nil, fmt.Errorf("%w: select only works on <select> elements, not on <%s>", ErrUnexpectedTagName, tagName)
	}
	multipleValue, err := element.GetAttribute("multiple")
	if err != nil {
		return nil, err
	}
	return &Select{
		element:    element,
		isMultiple: truthy(multipleValue),
	}, nil
}

func (s *Select) Options() ([]*WebElement, error) {
	return s.element.FindElements(ByTagName, "option")
}

func (s *Select) AllSelectedOptions() ([]*WebElement, error) {
	options, err := s.Options()
	if err != nil {
		return nil, err
	}
	result := make([]*WebElement, 0)
	for _, option := range options {
		selected, err := option.GetAttribute("selected")
		if err != nil {
			return nil, err
		}
		if truthy(selected) {
			result = append(result, option)
		}
	}
	return result, nil
}

func (s *Select) FirstSelectedOption() (*WebElement, error) {
	options, err := s.Options()
	if err != nil {
		return nil, err
	}
	for _, option := range options {
		selected, err := option.GetAttribute("selected")
		if err != nil {
			return nil, err
		}
		if truthy(selected) {
			return option, nil
		}
	}
	return nil, fmt.Errorf("%w: no options are selected", ErrNoSuchElement)
}

func (s *Select) SelectByValue(value string) error {
	options, err := s.element.FindElements(ByCSSSelector, fmt.Sprintf("option[value='%s']", value))
	if err != nil {
		return err
	}
	matched := false
	for _, option := range options {
		if err := s.setSelected(option); err != nil {
			return err
		}
		if !s.isMultiple {
			return nil
		}
		matched = true
	}
	if !matched {
		return fmt.Errorf("%w: cannot locate option with value: %s", ErrNoSuchElement, value)
	}
	return nil
}

func (s *Select) SelectByIndex(index any) error {
	options, err := s.Options()
	if err != nil {
		return err
	}
	for _, option := range options {
		attr, err := option.GetAttribute("index")
		if err != nil {
			return err
		}
		if fmt.Sprintf("%v", attr) == fmt.Sprintf("%v", index) {
			return s.setSelected(option)
		}
	}
	return fmt.Errorf("%w: could not locate element with index %v", ErrNoSuchElement, index)
}

func (s *Select) SelectByVisibleText(text string) error {
	options, err := s.Options()
	if err != nil {
		return err
	}
	matched := false
	for _, option := range options {
		html, err := option.InnerHTML()
		if err != nil {
			return err
		}
		if html == text {
			if err := s.setSelected(option); err != nil {
				return err
			}
			if !s.isMultiple {
				return nil
			}
			matched = true
		}
	}
	if !matched {
		return fmt.Errorf("%w: could not locate element with visible text: %s", ErrNoSuchElement, text)
	}
	return nil
}

func (s *Select) DeselectAll() error {
	if !s.isMultiple {
		return errors.New("you may only deselect all options of a multi-select")
	}
	options, err := s.Options()
	if err != nil {
		return err
	}
	for _, option := range options {
		if err := s.unsetSelected(option); err != nil {
			return err
		}
	}
	return nil
}

func (s *Select) DeselectByValue(value string) error {
	if !s.isMultiple {
		return errors.New("you may only deselect options of a multi-select")
	}
	options, err := s.element.FindElements(ByCSSSelector, fmt.Sprintf("option[value='%s']", value))
	if err != nil {
		return err
	}
	matched := false
	for _, option := range options {
		if err := s.unsetSelected(option); err != nil {
			return err
		}
		matched = true
	}
	if !matched {
		return fmt.Errorf("%w: could not locate element with value: %s", ErrNoSuchElement, value)
	}
	return nil
}

func (s *Select) DeselectByIndex(index any) error {
	if !s.isMultiple {
		return errors.New("you may only deselect options of a multi-select")
	}
	options, err := s.Options()
	if err != nil {
		return err
	}
	for _, option := range options {
		attr, err := option.GetAttribute("index")
		if err != nil {
			return err
		}
		if fmt.Sprintf("%v", attr) == fmt.Sprintf("%v", index) {
			return s.unsetSelected(option)
		}
	}
	return fmt.Errorf("%w: could not locate element with index %v", ErrNoSuchElement, index)
}

func (s *Select) DeselectByVisibleText(text string) error {
	if !s.isMultiple {
		return errors.New("you may only deselect options of a multi-select")
	}
	options, err := s.Options()
	if err != nil {
		return err
	}
	matched := false
	for _, option := range options {
		html, err := option.InnerHTML()
		if err != nil {
			return err
		}
		if html == text {
			if err := s.unsetSelected(option); err != nil {
				return err
			}
			matched = true
		}
	}
	if !matched {
		return fmt.Errorf("%w: could not locate element with visible text: %s", ErrNoSuchElement, text)
	}
	return nil
}

func (s *Select) setSelected(option *WebElement) error {
	selected, err := option.GetAttribute("selected")
	if err != nil {
		return err
	}
	if !truthy(selected) {
		_, err = option.SetAttribute("selected", "true", false)
		return err
	}
	return nil
}

func (s *Select) unsetSelected(option *WebElement) error {
	selected, err := option.GetAttribute("selected")
	if err != nil {
		return err
	}
	if truthy(selected) {
		_, err = option.SetAttribute("selected", "false", false)
		return err
	}
	return nil
}

func encodeRequestMap(data map[string]any) []byte {
	encoded := make(map[string]any, len(data))
	for key, value := range data {
		encoded[key] = encodeRequestValue(value)
	}
	return []byte(pythonLiteral(encoded))
}

func encodeRequestValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, inner := range typed {
			out[key] = encodeRequestValue(inner)
		}
		return out
	case map[string]string:
		out := make(map[string]any, len(typed))
		for key, inner := range typed {
			out[key] = encodeRequestLeaf(inner)
		}
		return out
	default:
		return encodeRequestLeaf(value)
	}
}

func encodeRequestLeaf(value any) string {
	return base64.StdEncoding.EncodeToString([]byte(pythonScalar(value)))
}

func pythonScalar(value any) string {
	switch typed := value.(type) {
	case nil:
		return "None"
	case string:
		return typed
	case bool:
		if typed {
			return "True"
		}
		return "False"
	default:
		return fmt.Sprintf("%v", value)
	}
}

func pythonLiteral(value any) string {
	switch typed := value.(type) {
	case nil:
		return "None"
	case string:
		return "'" + escapePythonString(typed) + "'"
	case bool:
		if typed {
			return "True"
		}
		return "False"
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			parts = append(parts, fmt.Sprintf("%s: %s", pythonLiteral(key), pythonLiteral(typed[key])))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case map[string]string:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			parts = append(parts, fmt.Sprintf("%s: %s", pythonLiteral(key), pythonLiteral(typed[key])))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			parts = append(parts, pythonLiteral(item))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case []string:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			parts = append(parts, pythonLiteral(item))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	default:
		rv := reflect.ValueOf(value)
		switch rv.Kind() {
		case reflect.Map:
			keys := rv.MapKeys()
			keyStrings := make([]string, 0, len(keys))
			keyIndex := make(map[string]reflect.Value, len(keys))
			for _, key := range keys {
				name := fmt.Sprintf("%v", key.Interface())
				keyStrings = append(keyStrings, name)
				keyIndex[name] = key
			}
			sort.Strings(keyStrings)
			parts := make([]string, 0, len(keys))
			for _, key := range keyStrings {
				parts = append(parts, fmt.Sprintf("%s: %s", pythonLiteral(key), pythonLiteral(rv.MapIndex(keyIndex[key]).Interface())))
			}
			return "{" + strings.Join(parts, ", ") + "}"
		case reflect.Slice, reflect.Array:
			parts := make([]string, 0, rv.Len())
			for i := 0; i < rv.Len(); i++ {
				parts = append(parts, pythonLiteral(rv.Index(i).Interface()))
			}
			return "[" + strings.Join(parts, ", ") + "]"
		default:
			return fmt.Sprintf("%v", value)
		}
	}
}

func escapePythonString(value string) string {
	replacer := strings.NewReplacer(
		"\\", "\\\\",
		"'", "\\'",
		"\n", "\\n",
		"\r", "\\r",
		"\t", "\\t",
	)
	return replacer.Replace(value)
}

func decodeResponse(raw string) (map[string]any, error) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, err
	}
	return decodeMap(payload), nil
}

func decodeMap(payload map[string]any) map[string]any {
	result := make(map[string]any, len(payload))
	for key, value := range payload {
		result[key] = decodeValue(value)
	}
	return result
}

func decodeValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return decodeMap(typed)
	case []any:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = decodeValue(item)
		}
		return out
	case string:
		return decodeBase64Value(typed)
	default:
		return typed
	}
}

func decodeBase64Value(value string) any {
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return value
	}
	raw := html.UnescapeString(string(decoded))
	raw = strings.ReplaceAll(raw, "\r", "\\r")
	raw = strings.ReplaceAll(raw, "\n", "\\n")

	switch raw {
	case "null", "undefined", "None":
		return nil
	case "true", "True":
		return true
	case "false", "False":
		return false
	}

	if parsed, ok := parseJSONArrayOrObject(raw); ok {
		return parsed
	}
	if number, ok := parseNumber(raw); ok {
		return number
	}
	return raw
}

func parseJSONArrayOrObject(raw string) (any, bool) {
	if raw == "" {
		return nil, false
	}
	first := raw[0]
	last := raw[len(raw)-1]
	if !((first == '[' && last == ']') || (first == '{' && last == '}') || (first == '"' && last == '"')) {
		return nil, false
	}
	var parsed any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, false
	}
	return decodeValue(parsed), true
}

func parseNumber(raw string) (any, bool) {
	if i, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return i, true
	}
	if f, err := strconv.ParseFloat(raw, 64); err == nil {
		return f, true
	}
	return nil, false
}

func responseCommand(response map[string]any) string {
	return asString(response["command"])
}

func asString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return fmt.Sprintf("%v", value)
	}
}

func asFloat64(value any) float64 {
	switch typed := value.(type) {
	case nil:
		return 0
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int8:
		return float64(typed)
	case int16:
		return float64(typed)
	case int32:
		return float64(typed)
	case int64:
		return float64(typed)
	case uint:
		return float64(typed)
	case uint8:
		return float64(typed)
	case uint16:
		return float64(typed)
	case uint32:
		return float64(typed)
	case uint64:
		return float64(typed)
	case json.Number:
		number, _ := typed.Float64()
		return number
	case string:
		number, _ := strconv.ParseFloat(typed, 64)
		return number
	default:
		number, _ := strconv.ParseFloat(fmt.Sprintf("%v", value), 64)
		return number
	}
}

func asInt(value any) int {
	switch typed := value.(type) {
	case nil:
		return 0
	case int:
		return typed
	case int8:
		return int(typed)
	case int16:
		return int(typed)
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case uint:
		return int(typed)
	case uint8:
		return int(typed)
	case uint16:
		return int(typed)
	case uint32:
		return int(typed)
	case uint64:
		return int(typed)
	case float32:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		number, _ := typed.Int64()
		return int(number)
	case string:
		number, _ := strconv.Atoi(typed)
		return number
	default:
		number, _ := strconv.Atoi(fmt.Sprintf("%v", value))
		return number
	}
}

func asSlice(value any) ([]any, bool) {
	switch typed := value.(type) {
	case []any:
		return typed, true
	default:
		return nil, false
	}
}

func asMap(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return typed, true
	default:
		return nil, false
	}
}

func copyMap(input map[string]any) map[string]any {
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func truthy(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case string:
		return typed != ""
	case int:
		return typed != 0
	case int8:
		return typed != 0
	case int16:
		return typed != 0
	case int32:
		return typed != 0
	case int64:
		return typed != 0
	case uint:
		return typed != 0
	case uint8:
		return typed != 0
	case uint16:
		return typed != 0
	case uint32:
		return typed != 0
	case uint64:
		return typed != 0
	case float32:
		return typed != 0
	case float64:
		return typed != 0
	case []any:
		return len(typed) > 0
	case map[string]any:
		return len(typed) > 0
	default:
		rv := reflect.ValueOf(value)
		switch rv.Kind() {
		case reflect.Slice, reflect.Array, reflect.Map, reflect.String:
			return rv.Len() > 0
		case reflect.Bool:
			return rv.Bool()
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return rv.Int() != 0
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			return rv.Uint() != 0
		case reflect.Float32, reflect.Float64:
			return rv.Float() != 0
		default:
			return true
		}
	}
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := writer.Write(data)
		if err != nil {
			return err
		}
		data = data[n:]
	}
	return nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func randomChoice(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[rand.IntN(len(values))]
}

func (d *WebDriver) captureNavigationSnapshot() (navigationSnapshot, error) {
	result, err := d.ExecuteScript(`(() => {
		const root = document.documentElement;
		return {
			url: location.href || "",
			title: document.title || "",
			ready_state: document.readyState || "",
			html_size: root ? root.outerHTML.length : 0
		};
	})()`)
	if err != nil {
		return navigationSnapshot{}, err
	}
	data, ok := asMap(result)
	if !ok {
		return navigationSnapshot{}, fmt.Errorf("unexpected navigation snapshot payload: %T", result)
	}
	return navigationSnapshot{
		URL:        asString(data["url"]),
		Title:      asString(data["title"]),
		ReadyState: strings.ToLower(asString(data["ready_state"])),
		HTMLSize:   asInt(data["html_size"]),
	}, nil
}

func (d *WebDriver) captureAndStoreNavigationSnapshot() error {
	snapshot, err := d.captureNavigationSnapshot()
	if err != nil {
		return err
	}
	d.storeNavigationSnapshot(snapshot)
	return nil
}

func (d *WebDriver) storeNavigationSnapshot(snapshot navigationSnapshot) {
	d.navMu.Lock()
	defer d.navMu.Unlock()
	d.lastNavigation = snapshot
	d.hasLastNavigation = true
}

func (d *WebDriver) lastNavigationSnapshotValue() (navigationSnapshot, bool) {
	d.navMu.Lock()
	defer d.navMu.Unlock()
	return d.lastNavigation, d.hasLastNavigation
}

func (d *WebDriver) navigationChanged(previous, current navigationSnapshot) bool {
	return previous.URL != current.URL ||
		previous.Title != current.Title ||
		previous.HTMLSize != current.HTMLSize
}

func (d *WebDriver) navigationInProgress(snapshot navigationSnapshot) bool {
	return snapshot.ReadyState == "loading" || snapshot.ReadyState == "interactive"
}

func (d *WebDriver) navigationLoaded(snapshot navigationSnapshot) bool {
	return snapshot.ReadyState == "complete" && strings.TrimSpace(snapshot.URL) != ""
}

func applyGeneratedIPHeaders(headers map[string]string) (string, string, string) {
	if headers == nil {
		return "", "", ""
	}
	ip, cc, name := generateIP()
	if strings.TrimSpace(ip) == "" {
		return "", cc, name
	}
	headers["X-Forwarded-For"] = ip
	headers["HTTP_X_FORWARDED_FOR"] = ip
	headers["X-CLIENT-IP"] = ip
	headers["X-Real-IP"] = ip
	headers["REMOTE_ADDR"] = ip
	return ip, cc, name
}

func generateIP() (string, string, string) {
	ip, cc, name, err := randomPublicIPFromDB(defaultIP2ASNFile)
	if err == nil && strings.TrimSpace(ip) != "" {
		return ip, cc, name
	}
	return randomPublicIPv4Fallback(), "", ""
}

func randomPublicIPv4Fallback() string {
	for attempts := 0; attempts < 1024; attempts++ {
		addr := netip.AddrFrom4([4]byte{
			byte(rand.IntN(223) + 1),
			byte(rand.IntN(256)),
			byte(rand.IntN(256)),
			byte(rand.IntN(254) + 1),
		})
		if !isPublicIPv4(addr) {
			continue
		}
		return addr.String()
	}
	return ""
}

func loadIP2ASN(path string) (*ip2asnDB, error) {
	ip2asnLoadOnce.Do(func() {
		b, err := os.ReadFile(path)
		if err != nil {
			ip2asnLoadErr = err
			return
		}

		lines := strings.Split(string(b), "\n")
		db := &ip2asnDB{
			starts: make([]uint32, 0, len(lines)),
			ends:   make([]uint32, 0, len(lines)),
			ccs:    make([]string, 0, len(lines)),
			names:  make([]string, 0, len(lines)),
		}
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}

			parts := strings.SplitN(line, "\t", 5)
			if len(parts) < 5 {
				continue
			}

			start, err1 := strconv.ParseUint(strings.TrimSpace(parts[0]), 10, 32)
			end, err2 := strconv.ParseUint(strings.TrimSpace(parts[1]), 10, 32)
			if err1 != nil || err2 != nil {
				continue
			}

			db.starts = append(db.starts, uint32(start))
			db.ends = append(db.ends, uint32(end))
			db.ccs = append(db.ccs, strings.TrimSpace(parts[3]))
			db.names = append(db.names, strings.TrimSpace(parts[4]))
		}
		ip2asnData = db
	})

	if ip2asnLoadErr != nil {
		return nil, ip2asnLoadErr
	}
	if ip2asnData == nil || len(ip2asnData.starts) == 0 {
		return nil, errors.New("empty ip2asn dataset")
	}
	return ip2asnData, nil
}

func randomPublicIPFromDB(path string) (string, string, string, error) {
	db, err := loadIP2ASN(path)
	if err != nil {
		return "", "", "", err
	}

	for attempts := 0; attempts < 1000; attempts++ {
		i := rand.IntN(len(db.starts))
		start := db.starts[i]
		end := db.ends[i]
		if end < start {
			start, end = end, start
		}
		if start == end {
			addr := uint32ToIPv4Addr(start)
			if isPublicIPv4(addr) {
				return addr.String(), db.ccs[i], db.names[i], nil
			}
			continue
		}

		ipInt := start + uint32(rand.Uint64N(uint64(end-start)+1))
		addr := uint32ToIPv4Addr(ipInt)
		if !isPublicIPv4(addr) {
			continue
		}
		cc := ""
		if i < len(db.ccs) {
			cc = db.ccs[i]
		}
		name := ""
		if i < len(db.names) {
			name = db.names[i]
		}
		return addr.String(), cc, name, nil
	}

	return "", "", "", errors.New("failed to sample public ip from ip2asn")
}

func uint32ToIPv4Addr(v uint32) netip.Addr {
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}

func isPublicIPv4(addr netip.Addr) bool {
	if !addr.IsValid() || !addr.Is4() {
		return false
	}
	if !addr.IsGlobalUnicast() {
		return false
	}
	if addr.IsPrivate() || addr.IsLoopback() || addr.IsMulticast() || addr.IsLinkLocalUnicast() || addr.IsUnspecified() {
		return false
	}
	// Exclude carrier-grade NAT 100.64.0.0/10.
	if addr.Compare(netip.MustParseAddr("100.64.0.0")) >= 0 && addr.Compare(netip.MustParseAddr("100.127.255.255")) <= 0 {
		return false
	}
	return true
}

func extractChromeVersion(userAgent string) string {
	matches := chromeVersionPattern.FindStringSubmatch(userAgent)
	if len(matches) > 1 && matches[1] != "" {
		return matches[1]
	}
	return LatestStableChromeVersion
}

func extractChromeMajorVersion(fullVersion string) string {
	if fullVersion == "" {
		return strings.SplitN(LatestStableChromeVersion, ".", 2)[0]
	}
	parts := strings.SplitN(fullVersion, ".", 2)
	return parts[0]
}

func extractAndroidVersion(userAgent string) string {
	matches := androidUAPattern.FindStringSubmatch(userAgent)
	if len(matches) > 1 && matches[1] != "" {
		return normalizePlatformVersion(strings.ReplaceAll(matches[1], "_", "."))
	}
	return "15.0.0"
}

func extractAndroidModel(userAgent string) string {
	matches := androidUAPattern.FindStringSubmatch(userAgent)
	if len(matches) > 2 {
		model := strings.TrimSpace(matches[2])
		if model != "" {
			return strings.TrimSuffix(model, " Build")
		}
	}
	return "Android"
}

func normalizePlatformVersion(version string) string {
	if version == "" {
		return "15.0.0"
	}
	parts := strings.Split(version, ".")
	for len(parts) < 3 {
		parts = append(parts, "0")
	}
	return strings.Join(parts[:3], ".")
}

func delayWithWriter(writer io.Writer, seconds int) {
	if writer == nil {
		writer = os.Stdout
	}
	if seconds <= 0 {
		_, _ = fmt.Fprint(writer, "\033[32m[ok]\033[0m \033[37mdelay 0s\033[0m\n")
		return
	}

	total := time.Duration(seconds) * time.Second
	start := time.Now()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	frames := []string{".  ", ".. ", "...", " ..", "  ."}
	index := 0

	for {
		elapsed := time.Since(start)
		if elapsed >= total {
			break
		}
		remaining := total - elapsed
		_, _ = fmt.Fprintf(
			writer,
			"\r\033[2m[%s]\033[0m \033[36mdelay\033[0m \033[37m%s remaining\033[0m",
			frames[index],
			formatCountdown(remaining),
		)
		<-ticker.C
		index = (index + 1) % len(frames)
	}

	_, _ = fmt.Fprintf(writer, "\r\033[32m[ok]\033[0m \033[37mdelay %s completed\033[0m\n", formatCountdown(total))
}

func SafeKillCurrentProcess(sig syscall.Signal, fallbackAfter time.Duration) error {
	return safeKillPID(os.Getpid(), sig, fallbackAfter)
}

func forceStopTarget(target string) error {
	pkg := normalizeAndroidTargetPackage(target)
	if pkg == "" {
		return errors.New("android target package is empty")
	}
	cmd := exec.Command("am", "force-stop", pkg)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

func normalizeAndroidTargetPackage(target string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return ""
	}
	if slash := strings.IndexByte(target, '/'); slash >= 0 {
		return strings.TrimSpace(target[:slash])
	}
	return target
}

func safeKillPID(pid int, sig syscall.Signal, fallbackAfter time.Duration) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid: %d", pid)
	}
	if sig == 0 {
		sig = syscall.SIGTERM
	}
	if fallbackAfter > 0 {
		go func() {
			time.Sleep(fallbackAfter)
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}()
	}
	signal.Reset(sig, os.Interrupt, syscall.SIGHUP)
	return syscall.Kill(pid, sig)
}

func formatCountdown(duration time.Duration) string {
	if duration < 0 {
		duration = 0
	}
	if duration < time.Second {
		return fmt.Sprintf("%dms", duration.Milliseconds())
	}
	seconds := duration.Seconds()
	return fmt.Sprintf("%.1fs", seconds)
}

func (d *WebDriver) waitForReadyState(timeout time.Duration, requireComplete bool) (string, error) {
	if timeout <= 0 {
		timeout = d.navigationTimeout
	}

	deadline := time.Now().Add(timeout)
	for {
		stateValue, err := d.ExecuteScript("document.readyState")
		if err == nil {
			state := strings.ToLower(asString(stateValue))
			if state == "complete" || (!requireComplete && state == "interactive") {
				return state, nil
			}
		}

		if time.Now().After(deadline) {
			target := "interactive"
			if requireComplete {
				target = "complete"
			}
			return "", fmt.Errorf("time out waiting for document.readyState=%s", target)
		}
		time.Sleep(d.readyStatePoll)
	}
}

func (d *WebDriver) runProgressStep(message string, fn func() error) error {
	stop := d.startProgress(message)
	err := fn()
	stop(err)
	return err
}

func (d *WebDriver) startProgress(message string) func(error) {
	if !d.enableProgressLog || d.logWriter == nil {
		return func(error) {}
	}

	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		frames := []string{".  ", ".. ", "...", " ..", "  ."}
		ticker := time.NewTicker(120 * time.Millisecond)
		defer ticker.Stop()

		index := 0
		d.printLogLine("\r\033[2m[%s]\033[0m \033[36m%s\033[0m", frames[index], message)
		for {
			select {
			case err := <-done:
				if err != nil {
					d.printLogLine("\r\033[31m[!!]\033[0m \033[37m%s\033[0m\n", message)
				} else {
					d.printLogLine("\r\033[32m[ok]\033[0m \033[37m%s\033[0m\n", message)
				}
				return
			case <-ticker.C:
				index = (index + 1) % len(frames)
				d.printLogLine("\r\033[2m[%s]\033[0m \033[36m%s\033[0m", frames[index], message)
			}
		}
	}()

	return func(err error) {
		done <- err
		<-finished
	}
}

func (d *WebDriver) printLogLine(format string, args ...any) {
	d.logMu.Lock()
	defer d.logMu.Unlock()
	_, _ = fmt.Fprintf(d.logWriter, format, args...)
}

func (d *WebDriver) logStartup(message string) {
	if !d.enableProgressLog || d.logWriter == nil {
		return
	}
	d.logMu.Lock()
	defer d.logMu.Unlock()
	_, _ = fmt.Fprintf(d.logWriter, "\033[2m[startup]\033[0m \033[37m%s\033[0m\n", message)
}

func (d *WebDriver) logSnapshot(currentURL, title, userAgent string) {
	if !d.enableProgressLog || d.logWriter == nil {
		return
	}
	d.logMu.Lock()
	defer d.logMu.Unlock()
	_, _ = fmt.Fprintf(d.logWriter,
		"\033[2m[url]\033[0m \033[37m%s\033[0m\n\033[2m[title]\033[0m \033[37m%s\033[0m\n\033[2m[ua]\033[0m \033[37m%s\033[0m\n",
		currentURL,
		title,
		userAgent,
	)
}

func (d *WebDriver) logIPUsage(ip, cc, name string) {
	if !d.enableProgressLog || d.logWriter == nil || strings.TrimSpace(ip) == "" {
		return
	}
	parts := []string{ip}
	if strings.TrimSpace(cc) != "" {
		parts = append(parts, cc)
	}
	if strings.TrimSpace(name) != "" {
		parts = append(parts, name)
	}
	d.logMu.Lock()
	defer d.logMu.Unlock()
	_, _ = fmt.Fprintf(d.logWriter, "\033[2m[ip]\033[0m \033[37m%s\033[0m\n", strings.Join(parts, " | "))
}
