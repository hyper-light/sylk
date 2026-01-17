package security

import (
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"sync"
	"sync/atomic"
)

type NetworkProxyStats struct {
	RequestsAllowed uint64
	RequestsBlocked uint64
	TotalRequests   uint64
}

type NetworkProxy struct {
	mu             sync.RWMutex
	allowedDomains map[string]bool
	listener       net.Listener
	server         *http.Server
	running        bool
	stats          NetworkProxyStats
	port           int
}

func NewNetworkProxy(allowedDomains []string) *NetworkProxy {
	np := &NetworkProxy{
		allowedDomains: make(map[string]bool),
	}

	for _, d := range allowedDomains {
		np.allowedDomains[d] = true
	}

	np.addDefaultDomains()

	return np
}

func (np *NetworkProxy) addDefaultDomains() {
	defaults := []string{
		"registry.npmjs.org",
		"pypi.org",
		"files.pythonhosted.org",
		"pkg.go.dev",
		"proxy.golang.org",
		"sum.golang.org",
		"crates.io",
		"static.crates.io",
		"rubygems.org",
		"api.nuget.org",
		"repo1.maven.org",
		"central.maven.org",
		"github.com",
		"api.github.com",
		"raw.githubusercontent.com",
		"docs.python.org",
		"docs.rs",
		"doc.rust-lang.org",
		"pkg.go.dev",
		"docs.oracle.com",
		"developer.mozilla.org",
	}

	for _, d := range defaults {
		np.allowedDomains[d] = true
	}
}

func (np *NetworkProxy) Start() error {
	np.mu.Lock()
	defer np.mu.Unlock()

	if np.running {
		return nil
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}

	np.listener = listener
	np.port = listener.Addr().(*net.TCPAddr).Port

	np.server = &http.Server{
		Handler: http.HandlerFunc(np.handleRequest),
	}

	go np.server.Serve(listener)
	np.running = true

	return nil
}

func (np *NetworkProxy) handleRequest(w http.ResponseWriter, r *http.Request) {
	atomic.AddUint64(&np.stats.TotalRequests, 1)

	host := r.Host
	if host == "" {
		host = r.URL.Host
	}

	if np.isDomainAllowed(host) {
		atomic.AddUint64(&np.stats.RequestsAllowed, 1)
		np.proxyRequest(w, r)
		return
	}

	atomic.AddUint64(&np.stats.RequestsBlocked, 1)
	http.Error(w, "domain blocked by sandbox policy", http.StatusForbidden)
}

func (np *NetworkProxy) isDomainAllowed(host string) bool {
	np.mu.RLock()
	defer np.mu.RUnlock()

	hostname, _, err := net.SplitHostPort(host)
	if err != nil {
		hostname = host
	}

	return np.allowedDomains[hostname]
}

func (np *NetworkProxy) proxyRequest(w http.ResponseWriter, r *http.Request) {
	resp, err := http.DefaultTransport.RoundTrip(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	np.copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)

	buf := make([]byte, 32*1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			w.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
}

func (np *NetworkProxy) copyHeaders(dst, src http.Header) {
	for k, v := range src {
		for _, vv := range v {
			dst.Add(k, vv)
		}
	}
}

func (np *NetworkProxy) Stop() {
	np.mu.Lock()
	defer np.mu.Unlock()

	if !np.running {
		return
	}

	if np.server != nil {
		np.server.Close()
	}
	if np.listener != nil {
		np.listener.Close()
	}

	np.running = false
}

func (np *NetworkProxy) RouteCommand(cmd *exec.Cmd) {
	np.mu.RLock()
	defer np.mu.RUnlock()

	if !np.running || np.port == 0 {
		return
	}

	proxyURL := fmt.Sprintf("http://127.0.0.1:%d", np.port)

	if cmd.Env == nil {
		cmd.Env = []string{}
	}

	cmd.Env = append(cmd.Env,
		"HTTP_PROXY="+proxyURL,
		"HTTPS_PROXY="+proxyURL,
		"http_proxy="+proxyURL,
		"https_proxy="+proxyURL,
	)
}

func (np *NetworkProxy) AddAllowedDomain(domain string) {
	np.mu.Lock()
	defer np.mu.Unlock()
	np.allowedDomains[domain] = true
}

func (np *NetworkProxy) RemoveAllowedDomain(domain string) {
	np.mu.Lock()
	defer np.mu.Unlock()
	delete(np.allowedDomains, domain)
}

func (np *NetworkProxy) ListAllowedDomains() []string {
	np.mu.RLock()
	defer np.mu.RUnlock()

	domains := make([]string, 0, len(np.allowedDomains))
	for d := range np.allowedDomains {
		domains = append(domains, d)
	}
	return domains
}

func (np *NetworkProxy) GetStats() NetworkProxyStats {
	return NetworkProxyStats{
		RequestsAllowed: atomic.LoadUint64(&np.stats.RequestsAllowed),
		RequestsBlocked: atomic.LoadUint64(&np.stats.RequestsBlocked),
		TotalRequests:   atomic.LoadUint64(&np.stats.TotalRequests),
	}
}

func (np *NetworkProxy) ResetStats() {
	atomic.StoreUint64(&np.stats.RequestsAllowed, 0)
	atomic.StoreUint64(&np.stats.RequestsBlocked, 0)
	atomic.StoreUint64(&np.stats.TotalRequests, 0)
}

func (np *NetworkProxy) IsRunning() bool {
	np.mu.RLock()
	defer np.mu.RUnlock()
	return np.running
}

func (np *NetworkProxy) Port() int {
	np.mu.RLock()
	defer np.mu.RUnlock()
	return np.port
}
