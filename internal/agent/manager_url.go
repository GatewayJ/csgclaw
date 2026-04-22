package agent

import (
	"fmt"
	"net"
	"os"
	"strings"

	"csgclaw/internal/config"
)

// localIPv4Resolver lets tests override IP discovery.
var localIPv4Resolver = localIPv4

// envK8sPodIP is optionally populated in Kubernetes-style deployments.
const envK8sPodIP = "POD_IP"

// resolveManagerBaseURL picks the URL that manager/worker sandboxes should
// use to reach the csgclaw server:
// AdvertiseBaseURL -> POD_IP -> discovered local IPv4.
func resolveManagerBaseURL(server config.ServerConfig) string {
	if url := strings.TrimRight(strings.TrimSpace(server.AdvertiseBaseURL), "/"); url != "" {
		return url
	}
	port := config.ListenPort(server.ListenAddr)
	if ip := strings.TrimSpace(os.Getenv(envK8sPodIP)); ip != "" {
		return fmt.Sprintf("http://%s:%s", ip, port)
	}
	if ip := localIPv4Resolver(); ip != "" {
		return fmt.Sprintf("http://%s:%s", ip, port)
	}
	return ""
}

func localIPv4() string {
	if ip := outboundIPv4(); ip != "" {
		return ip
	}
	return interfaceIPv4()
}

func outboundIPv4() string {
	conn, err := net.Dial("udp4", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer conn.Close()

	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || addr.IP == nil {
		return ""
	}
	ip := addr.IP.To4()
	if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
		return ""
	}
	return ip.String()
}

func interfaceIPv4() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if ip := ipv4FromAddr(addr); ip != "" {
				return ip
			}
		}
	}
	return ""
}

func ipv4FromAddr(addr net.Addr) string {
	switch v := addr.(type) {
	case *net.IPNet:
		ip := v.IP.To4()
		if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
			return ""
		}
		return ip.String()
	case *net.IPAddr:
		ip := v.IP.To4()
		if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
			return ""
		}
		return ip.String()
	default:
		return ""
	}
}
