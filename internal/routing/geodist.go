package routing

import (
	"net"
	"net/http"
	"strings"

	"github.com/schhorn7/mission1-cdn/internal/models"
)

var RegionDistanceMap = map[string][]string {
	"asia": {"asia", "singapore", "tokyo", "europe", "london", "americas", "newyork"},
	"singapore": {"singapore", "asia", "tokyo", "europe", "london", "americas", "newyork"},
	"americas": {"americas", "newyork", "us", "europe", "london", "asia", "singapoor"},
	"newyork": {"newyork", "americas", "us", "europe", "london", "asia", "singapore"},
	"europe": {"europe", "london", "frankfurt", "asia", "singapore", "americas", "newyork"},
	"london": {"london", "europe", "frankfurt", "americas", "newyork", "asia", "singapore"},
}

func DetectClientRegion(r *http.Request, defaultRegion string) (string, string) {
	tz := strings.TrimSpace(r.Header.Get("X-Clinet-Timezone"))
	if tz != "" {
		region := TimezoneToRegion(tz)
		if region != "" {
			return region, "Browser Timezone (" + tz + ")"
		}
	}

	if reg := r.Header.Get("X-User-Region"); reg != "" {
		return strings.TrimSpace(reg), "Header (X-User-Region)"
	}

	if country := r.Header.Get("CF-IPCountry"); country != "" {
		reg := CountryCodeToRegion(country)
		return reg, "Cloudflare Country (" + country + ")"
	}

	clientIP := getClientIP(r)

	if defaultRegion == "" {
		defaultRegion = "Asia"
	}

	return defaultRegion, "IP Default (" + clientIP + ")"
}

func TimezoneToRegion(tz string) string {
	tzLower := strings.ToLower(tz)
	switch {
	case strings.HasPrefix(tzLower, "asia/"), strings.HasPrefix(tzLower, "australia"), strings.HasPrefix(tzLower, "pacific/"):
		return "Asia"
	case strings.HasPrefix(tzLower, "americas/"), strings.HasPrefix(tzLower, "us/"), strings.HasPrefix(tzLower, "canada/"), strings.HasPrefix(tzLower, "brazil/"):
		return "Americas"
	case strings.HasPrefix(tzLower, "europe/"), strings.HasPrefix(tzLower, "africa"), strings.HasPrefix(tzLower, "atlantic/"):
		return "Europe"
	default: 
		return ""
	}
	 
}

func CountryCodeToRegion(cc string) string {
	cc = strings.ToUpper(strings.TrimSpace(cc))
	switch cc {
	case "US", "CA", "MX", "BR", "AR", "CL", "CO":
		return "Americas"
	case "GB", "FR", "DE", "IT", "ES", "NL", "SE", "PL", "CH":
		return "Europe"
	default: 
		return "Asia"
	}
}

func getClientIP(r *http.Request) string {
	forward := r.Header.Get("X-Forwarded-For")
	if forward != "" {
		parts := strings.Split(forward, ",")
		return strings.TrimSpace(parts[0])
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}

	return host
}

func SelectBestNode(clientRegion string, availableNodes []models.StorageNode, replicasNodeIDs []string) *models.StorageNode {
	if len(availableNodes) == 0 || len(replicasNodeIDs) == 0 {
		return nil
	}

	replicaSet := make(map[string]bool)
	for _, id := range replicasNodeIDs {
		replicaSet[id] = true 
	}

	var candidateNodes []models.StorageNode
	for _, node := range availableNodes {
		if replicaSet[node.NodeID] && strings.EqualFold(node.Status, "active") {
			candidateNodes = append(candidateNodes, node)
		}
	}

	if len(candidateNodes) == 0 {
		return nil
	}

	cleanRegion := strings.ToLower(strings.TrimSpace(clientRegion))
	if cleanRegion == "" {
		return &candidateNodes[0]
	}

	for i := range candidateNodes {
		nodeRegion := strings.ToLower(candidateNodes[i].Region)
		if nodeRegion == cleanRegion || strings.Contains(nodeRegion, cleanRegion) {
			return &candidateNodes[i]
		}
	}

	if priorities, ok := RegionDistanceMap[cleanRegion]; ok {
		for _, targetRegion := range priorities {
			for i := range candidateNodes {
				nodeRegion := strings.ToLower(candidateNodes[i].Region)
				if strings.Contains(nodeRegion, targetRegion) || strings.Contains(targetRegion, nodeRegion) {
					return &candidateNodes[i]
				}
			}
		}
	}

	return &candidateNodes[0]
}