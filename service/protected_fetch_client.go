package service

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"golang.org/x/net/proxy"
)

type ssrfResolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

type protectedFetchDialer struct {
	resolver      ssrfResolver
	dialContext   func(ctx context.Context, network, address string) (net.Conn, error)
	getProtection func() (*common.SSRFProtection, bool, error)
	ipv4Only      bool
}

type ssrfProtectedRoundTripper struct {
	resolver      ssrfResolver
	dialContext   func(ctx context.Context, network, address string) (net.Conn, error)
	getProtection func() (*common.SSRFProtection, bool, error)
	ipv4Only      bool
	proxy         func(*http.Request) (*url.URL, error)
	validateURL   func(string) error

	mutex      sync.Mutex
	transports map[string]*http.Transport
}

func currentFetchProtection() (*common.SSRFProtection, bool, error) {
	fetchSetting := system_setting.GetFetchSetting()
	if !fetchSetting.EnableSSRFProtection {
		return nil, false, nil
	}

	protection, err := common.NewSSRFProtectionFromFetchSetting(
		fetchSetting.AllowPrivateIp,
		fetchSetting.DomainFilterMode,
		fetchSetting.IpFilterMode,
		fetchSetting.DomainList,
		fetchSetting.IpList,
		fetchSetting.AllowedPorts,
		fetchSetting.ApplyIPFilterForDomain,
	)
	if err != nil {
		return nil, true, err
	}
	return protection, true, nil
}

func currentImageCacheProtection() (*common.SSRFProtection, bool, error) {
	fetchSetting := system_setting.GetFetchSetting()
	if !fetchSetting.EnableSSRFProtection {
		return nil, false, nil
	}

	protection, err := common.NewSSRFProtectionFromFetchSetting(
		fetchSetting.AllowPrivateIp,
		fetchSetting.DomainFilterMode,
		fetchSetting.IpFilterMode,
		fetchSetting.DomainList,
		fetchSetting.IpList,
		imageCacheAllowedPorts(fetchSetting.AllowedPorts),
		fetchSetting.ApplyIPFilterForDomain,
	)
	if err != nil {
		return nil, true, err
	}
	return protection, true, nil
}

func newProtectedFetchHTTPClient() *http.Client {
	return newProtectedFetchHTTPClientWithDialer(nil, nil, nil)
}

func newImageCacheProtectedFetchHTTPClient() *http.Client {
	return newProtectedFetchHTTPClientWithProxyAndValidator(
		nil,
		nil,
		currentImageCacheProtection,
		http.ProxyFromEnvironment,
		ValidateImageCacheFetchURL,
	)
}

func newImageCacheProtectedProxyHTTPClient(proxyURL *url.URL) (*http.Client, error) {
	return newProtectedFetchProxyHTTPClient(proxyURL, currentImageCacheProtection, ValidateImageCacheFetchURL)
}

// newProtectedFetchProxyHTTPClient builds a protected client for a caller
// supplied URL validator and SSRF policy. Keeping proxy handling here makes
// specialized cache policies use the same redirect and dial-time checks as
// the regular protected client.
func newProtectedFetchProxyHTTPClient(proxyURL *url.URL, getProtection func() (*common.SSRFProtection, bool, error), validateURL func(string) error) (*http.Client, error) {
	return newProtectedFetchProxyHTTPClientWithFamily(proxyURL, getProtection, validateURL, false)
}

// newProtectedFetchProxyHTTPClientIPv4 is used by video caching, where the
// server's IPv6 route is unreliable. It keeps the proxy and SSRF policies
// unchanged while constraining outbound sockets to IPv4.
func newProtectedFetchProxyHTTPClientIPv4(proxyURL *url.URL, getProtection func() (*common.SSRFProtection, bool, error), validateURL func(string) error) (*http.Client, error) {
	return newProtectedFetchProxyHTTPClientWithFamily(proxyURL, getProtection, validateURL, true)
}

func newProtectedFetchProxyHTTPClientWithFamily(proxyURL *url.URL, getProtection func() (*common.SSRFProtection, bool, error), validateURL func(string) error, ipv4Only bool) (*http.Client, error) {
	if proxyURL == nil {
		return nil, fmt.Errorf("proxy URL is nil")
	}
	dialContext := (func(context.Context, string, string) (net.Conn, error))(nil)
	if ipv4Only {
		dialContext = dialIPv4Only
	}
	switch proxyURL.Scheme {
	case "http", "https":
		return newProtectedFetchHTTPClientWithProxyAndValidatorMode(
			nil,
			dialContext,
			getProtection,
			http.ProxyURL(proxyURL),
			validateURL,
			ipv4Only,
		), nil
	case "socks5", "socks5h":
		var auth *proxy.Auth
		if proxyURL.User != nil {
			auth = &proxy.Auth{User: proxyURL.User.Username()}
			if password, ok := proxyURL.User.Password(); ok {
				auth.Password = password
			}
		}
		dialer, err := proxy.SOCKS5("tcp", proxyURL.Host, auth, proxy.Direct)
		if err != nil {
			return nil, err
		}
		return newProtectedFetchHTTPClientWithProxyAndValidatorMode(
			nil,
			func(ctx context.Context, network, address string) (net.Conn, error) {
				if ipv4Only {
					network = "tcp4"
				}
				return dialer.Dial(network, address)
			},
			getProtection,
			func(req *http.Request) (*url.URL, error) { return nil, nil },
			validateURL,
			ipv4Only,
		), nil
	default:
		return nil, fmt.Errorf("unsupported proxy scheme: %s, must be http, https, socks5 or socks5h", proxyURL.Scheme)
	}
}

func newProtectedFetchHTTPClientWithDialer(resolver ssrfResolver, dialContext func(ctx context.Context, network, address string) (net.Conn, error), getProtection func() (*common.SSRFProtection, bool, error)) *http.Client {
	return newProtectedFetchHTTPClientWithProxy(resolver, dialContext, getProtection, http.ProxyFromEnvironment)
}

func newProtectedFetchHTTPClientWithProxy(resolver ssrfResolver, dialContext func(ctx context.Context, network, address string) (net.Conn, error), getProtection func() (*common.SSRFProtection, bool, error), proxy func(*http.Request) (*url.URL, error)) *http.Client {
	return newProtectedFetchHTTPClientWithProxyAndValidator(resolver, dialContext, getProtection, proxy, nil)
}

func newProtectedFetchHTTPClientWithProxyAndValidator(resolver ssrfResolver, dialContext func(ctx context.Context, network, address string) (net.Conn, error), getProtection func() (*common.SSRFProtection, bool, error), proxy func(*http.Request) (*url.URL, error), validateURL func(string) error) *http.Client {
	return newProtectedFetchHTTPClientWithProxyAndValidatorMode(resolver, dialContext, getProtection, proxy, validateURL, false)
}

func newProtectedFetchHTTPClientWithProxyAndValidatorIPv4(resolver ssrfResolver, dialContext func(ctx context.Context, network, address string) (net.Conn, error), getProtection func() (*common.SSRFProtection, bool, error), proxy func(*http.Request) (*url.URL, error), validateURL func(string) error) *http.Client {
	return newProtectedFetchHTTPClientWithProxyAndValidatorMode(resolver, dialContext, getProtection, proxy, validateURL, true)
}

func newProtectedFetchHTTPClientWithProxyAndValidatorMode(resolver ssrfResolver, dialContext func(ctx context.Context, network, address string) (net.Conn, error), getProtection func() (*common.SSRFProtection, bool, error), proxy func(*http.Request) (*url.URL, error), validateURL func(string) error, ipv4Only bool) *http.Client {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	if dialContext == nil {
		netDialer := &net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}
		dialContext = netDialer.DialContext
		if ipv4Only {
			dialContext = dialIPv4Only
		}
	}
	if getProtection == nil {
		getProtection = currentFetchProtection
	}
	if proxy == nil {
		proxy = http.ProxyFromEnvironment
	}
	if validateURL == nil {
		validateURL = ValidateSSRFProtectedFetchURL
	}

	client := &http.Client{
		Transport: &ssrfProtectedRoundTripper{
			resolver:      resolver,
			dialContext:   dialContext,
			getProtection: getProtection,
			proxy:         proxy,
			validateURL:   validateURL,
			ipv4Only:      ipv4Only,
			transports:    make(map[string]*http.Transport),
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return checkProtectedFetchRedirectWithValidator(req, via, validateURL)
		},
	}
	if common.RelayTimeout != 0 {
		client.Timeout = time.Duration(common.RelayTimeout) * time.Second
	}
	return client
}

func dialIPv4Only(ctx context.Context, _ string, address string) (net.Conn, error) {
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	return dialer.DialContext(ctx, "tcp4", address)
}

func (t *ssrfProtectedRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil {
		return nil, fmt.Errorf("invalid request")
	}
	validateURL := t.validateURL
	if validateURL == nil {
		validateURL = ValidateSSRFProtectedFetchURL
	}
	if err := validateURL(req.URL.String()); err != nil {
		return nil, err
	}

	proxyURL, err := t.proxy(req)
	if err != nil {
		return nil, err
	}
	return t.transportFor(proxyURL).RoundTrip(req)
}

func (t *ssrfProtectedRoundTripper) CloseIdleConnections() {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	for _, transport := range t.transports {
		transport.CloseIdleConnections()
	}
}

func (t *ssrfProtectedRoundTripper) transportFor(proxyURL *url.URL) *http.Transport {
	// 只按代理地址分组：代理来自环境变量，取值有限，map 有界；
	// 目标 origin 是用户可控输入，不能作为缓存 key。
	key := "direct"
	if proxyURL != nil {
		key = proxyURL.String()
	}
	t.mutex.Lock()
	defer t.mutex.Unlock()

	if transport, ok := t.transports[key]; ok {
		return transport
	}

	transport := t.newTransport(proxyURL)
	t.transports[key] = transport
	return transport
}

func (t *ssrfProtectedRoundTripper) newTransport(proxyURL *url.URL) *http.Transport {
	dialContext := t.dialContext
	proxyFunc := http.ProxyURL(proxyURL)
	if proxyURL == nil {
		protectedDialer := &protectedFetchDialer{
			resolver:      t.resolver,
			dialContext:   t.dialContext,
			getProtection: t.getProtection,
			ipv4Only:      t.ipv4Only,
		}
		dialContext = protectedDialer.DialContext
		proxyFunc = nil
	}

	transport := &http.Transport{
		MaxIdleConns:        common.RelayMaxIdleConns,
		MaxIdleConnsPerHost: common.RelayMaxIdleConnsPerHost,
		IdleConnTimeout:     time.Duration(common.RelayIdleConnTimeout) * time.Second,
		ForceAttemptHTTP2:   true,
		Proxy:               proxyFunc,
		DialContext:         dialContext,
	}
	if common.TLSInsecureSkipVerify {
		transport.TLSClientConfig = common.InsecureTLSConfig
	}
	return transport
}

func (d *protectedFetchDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	protection, enabled, err := d.getProtection()
	if err != nil {
		return nil, err
	}
	if !enabled {
		return d.dialContext(ctx, network, addr)
	}

	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid dial address %s: %w", addr, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return nil, fmt.Errorf("invalid port: %s", portText)
	}
	if err := protection.ValidateNetworkTarget(host, port); err != nil {
		return nil, err
	}

	if ip := net.ParseIP(host); ip != nil {
		if d.ipv4Only && ip.To4() == nil {
			return nil, fmt.Errorf("IPv4-only fetch cannot dial IPv6 address %s", host)
		}
		return d.dialContext(ctx, network, net.JoinHostPort(ip.String(), portText))
	}
	if !protection.ApplyIPFilterForDomain {
		return d.dialContext(ctx, network, addr)
	}

	resolved, err := d.resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("DNS resolution failed for %s: %v", host, err)
	}

	// Prefer IPv4 when both families are available. Some hosts expose IPv6
	// addresses while the server has no IPv6 route; trying those first causes a
	// recoverable CDN download to fail before IPv4 is attempted. IPv6 remains a
	// fallback for IPv6-only origins.
	var candidateIPv4, candidateIPv6 []net.IP
	for _, ipAddr := range resolved {
		ip := ipAddr.IP
		if ip == nil || !networkAllowsIP(network, ip) {
			continue
		}
		if d.ipv4Only && ip.To4() == nil {
			continue
		}
		if err := protection.ValidateResolvedIP(host, ip); err != nil {
			return nil, err
		}
		if ip.To4() != nil {
			candidateIPv4 = append(candidateIPv4, ip)
		} else {
			candidateIPv6 = append(candidateIPv6, ip)
		}
	}
	candidateIPs := append(candidateIPv4, candidateIPv6...)

	var lastDialErr error
	for _, ip := range candidateIPs {
		conn, err := d.dialContext(ctx, network, net.JoinHostPort(ip.String(), portText))
		if err == nil {
			return conn, nil
		}
		lastDialErr = err
	}

	if lastDialErr != nil {
		return nil, lastDialErr
	}
	return nil, fmt.Errorf("DNS resolution for %s returned no usable IP addresses", host)
}

func networkAllowsIP(network string, ip net.IP) bool {
	switch network {
	case "tcp4":
		return ip.To4() != nil
	case "tcp6":
		return ip.To4() == nil
	default:
		return true
	}
}
