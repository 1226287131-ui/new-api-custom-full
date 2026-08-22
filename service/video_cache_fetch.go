package service

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

// newVideoCacheHTTPClient creates the protected client used for a task's
// provider result. A channel origin may use a non-standard port, but that
// exception is bound to the exact configured channel origin below.
func newVideoCacheHTTPClient(source VideoCacheSource) (*http.Client, error) {
	getProtection := func() (*common.SSRFProtection, bool, error) {
		return currentVideoCacheProtection(source)
	}
	if strings.TrimSpace(source.TrustedOrigin) == "" {
		if strings.TrimSpace(source.Proxy) == "" {
			return newProtectedFetchHTTPClientWithProxyAndValidatorIPv4(
				nil,
				nil,
				getProtection,
				http.ProxyFromEnvironment,
				ValidateSSRFProtectedFetchURL,
			), nil
		}
		proxyURL, _, err := common.ParseProxyURLRuntime(source.Proxy)
		if err != nil {
			return nil, fmt.Errorf("parse video cache proxy: %w", err)
		}
		return newProtectedFetchProxyHTTPClientIPv4(proxyURL, getProtection, ValidateSSRFProtectedFetchURL)
	}

	validateURL := func(rawURL string) error {
		return validateVideoCacheFetchURL(source, rawURL)
	}
	if strings.TrimSpace(source.Proxy) == "" {
		return newProtectedFetchHTTPClientWithProxyAndValidatorIPv4(
			nil,
			nil,
			getProtection,
			http.ProxyFromEnvironment,
			validateURL,
		), nil
	}

	proxyURL, _, err := common.ParseProxyURLRuntime(source.Proxy)
	if err != nil {
		return nil, fmt.Errorf("parse video cache proxy: %w", err)
	}
	return newProtectedFetchProxyHTTPClientIPv4(proxyURL, getProtection, validateURL)
}

func currentVideoCacheProtection(source VideoCacheSource) (*common.SSRFProtection, bool, error) {
	fetchSetting := system_setting.GetFetchSetting()
	if !fetchSetting.EnableSSRFProtection {
		return nil, false, nil
	}

	allowedPorts := append([]string(nil), fetchSetting.AllowedPorts...)
	if port := trustedVideoCachePort(source); port != "" {
		allowedPorts = appendUniquePort(allowedPorts, port)
	}

	protection, err := common.NewSSRFProtectionFromFetchSetting(
		fetchSetting.AllowPrivateIp,
		fetchSetting.DomainFilterMode,
		fetchSetting.IpFilterMode,
		fetchSetting.DomainList,
		fetchSetting.IpList,
		allowedPorts,
		fetchSetting.ApplyIPFilterForDomain,
	)
	if err != nil {
		return nil, true, err
	}
	return protection, true, nil
}

// validateVideoCacheFetchURL keeps the normal fetch policy intact. It only
// retries validation with one extra port when the URL is on the exact origin
// configured for the channel; domain, IP and private-network checks are still
// enforced by the same SSRF validator.
func validateVideoCacheFetchURL(source VideoCacheSource, rawURL string) error {
	fetchSetting := system_setting.GetFetchSetting()
	if !fetchSetting.EnableSSRFProtection {
		return nil
	}

	validate := func(allowedPorts []string) error {
		return common.ValidateURLWithFetchSetting(
			rawURL,
			fetchSetting.EnableSSRFProtection,
			fetchSetting.AllowPrivateIp,
			fetchSetting.DomainFilterMode,
			fetchSetting.IpFilterMode,
			fetchSetting.DomainList,
			fetchSetting.IpList,
			allowedPorts,
			fetchSetting.ApplyIPFilterForDomain,
		)
	}

	if err := validate(fetchSetting.AllowedPorts); err == nil {
		return nil
	} else if !videoCacheURLMatchesTrustedOrigin(rawURL, source.TrustedOrigin) {
		return err
	}

	port := videoCacheURLPort(rawURL)
	if port == "" {
		return validate(fetchSetting.AllowedPorts)
	}
	allowedPorts := appendUniquePort(append([]string(nil), fetchSetting.AllowedPorts...), port)
	return validate(allowedPorts)
}

func trustedVideoCachePort(source VideoCacheSource) string {
	if !videoCacheURLMatchesTrustedOrigin(source.URL, source.TrustedOrigin) {
		return ""
	}
	return videoCacheURLPort(source.URL)
}

func videoCacheURLMatchesTrustedOrigin(rawURL, trustedOrigin string) bool {
	if strings.TrimSpace(rawURL) == "" || strings.TrimSpace(trustedOrigin) == "" {
		return false
	}
	target, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || !target.IsAbs() || target.Host == "" {
		return false
	}
	trusted, err := url.Parse(strings.TrimSpace(trustedOrigin))
	if err != nil || !trusted.IsAbs() || trusted.Host == "" {
		return false
	}
	return strings.EqualFold(target.Scheme, trusted.Scheme) &&
		strings.EqualFold(target.Hostname(), trusted.Hostname()) &&
		videoCacheParsedURLPort(target) == videoCacheParsedURLPort(trusted)
}

func videoCacheURLPort(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	return videoCacheParsedURLPort(parsed)
}

func videoCacheParsedURLPort(parsed *url.URL) string {
	if parsed == nil || !parsed.IsAbs() || parsed.Host == "" {
		return ""
	}
	if port := parsed.Port(); port != "" {
		return port
	}
	if strings.EqualFold(parsed.Scheme, "https") {
		return "443"
	}
	if strings.EqualFold(parsed.Scheme, "http") {
		return "80"
	}
	return ""
}

func appendUniquePort(ports []string, port string) []string {
	port = strings.TrimSpace(port)
	if port == "" {
		return ports
	}
	for _, existing := range ports {
		if strings.TrimSpace(existing) == port {
			return ports
		}
	}
	return append(ports, port)
}
