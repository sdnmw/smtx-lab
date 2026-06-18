package agent

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func (c Collector) DetectCNI() (CNISnapshot, error) {
	var snapshot CNISnapshot
	for _, dir := range c.CNIConfigDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := filepath.Join(dir, entry.Name())
			if !strings.HasSuffix(name, ".conf") && !strings.HasSuffix(name, ".conflist") && !strings.HasSuffix(name, ".json") {
				continue
			}
			b, err := os.ReadFile(name)
			if err != nil {
				snapshot.DetectionLog = append(snapshot.DetectionLog, "read "+name+": "+err.Error())
				continue
			}
			snapshot.ConfigFiles = append(snapshot.ConfigFiles, name)
			inspectCNIContent(&snapshot, strings.ToLower(string(b)))
		}
	}
	snapshot.Interfaces = detectInterfaces()
	inspectInterfaces(&snapshot, snapshot.Interfaces)
	if snapshot.Type == "" {
		snapshot.Type = "unknown"
	}
	sort.Strings(snapshot.ConfigFiles)
	sort.Strings(snapshot.Interfaces)
	sort.Strings(snapshot.Calico.WorkloadInterfaces)
	sort.Strings(snapshot.Calico.ConfigHints)
	finalizeCalicoOverlay(&snapshot)
	return snapshot, nil
}

func inspectCNIContent(snapshot *CNISnapshot, content string) {
	switch {
	case strings.Contains(content, "calico"):
		setCNI(snapshot, "calico", "iptables")
		addCalicoConfigHints(snapshot, content)
	case strings.Contains(content, "cilium"):
		setCNI(snapshot, "cilium", "ebpf")
	case strings.Contains(content, "everoute"):
		setCNI(snapshot, "everoute", "openvswitch")
	case strings.Contains(content, "flannel"):
		setCNI(snapshot, "flannel", "vxlan")
	}
}

func inspectInterfaces(snapshot *CNISnapshot, interfaces []string) {
	for _, iface := range interfaces {
		switch {
		case strings.HasPrefix(iface, "cali"):
			setCNI(snapshot, "calico", "iptables")
			snapshot.Calico.WorkloadInterfaces = append(snapshot.Calico.WorkloadInterfaces, iface)
		case iface == "tunl0":
			setCNI(snapshot, "calico", "iptables")
			snapshot.Calico.IPIPInterface = iface
		case iface == "vxlan.calico":
			setCNI(snapshot, "calico", "iptables")
			snapshot.Calico.VXLANInterface = iface
		case iface == "wireguard.cali":
			setCNI(snapshot, "calico", "iptables")
			snapshot.Calico.WireGuardInterface = iface
		case iface == "cilium_host" || iface == "cilium_net":
			setCNI(snapshot, "cilium", "ebpf")
		case strings.HasPrefix(iface, "flannel"):
			setCNI(snapshot, "flannel", "vxlan")
		case strings.HasPrefix(iface, "everoute"):
			setCNI(snapshot, "everoute", "openvswitch")
		}
	}
}

func setCNI(snapshot *CNISnapshot, cniType, mode string) {
	if snapshot.Type == "" || snapshot.Type == "unknown" {
		snapshot.Type = cniType
		snapshot.Mode = mode
	}
	snapshot.DetectionLog = append(snapshot.DetectionLog, "matched "+cniType)
}

func addCalicoConfigHints(snapshot *CNISnapshot, content string) {
	for _, hint := range []string{"ipip", "vxlan", "bgp", "crosssubnet", "wireguard"} {
		if strings.Contains(content, hint) && !containsString(snapshot.Calico.ConfigHints, hint) {
			snapshot.Calico.ConfigHints = append(snapshot.Calico.ConfigHints, hint)
		}
	}
}

func finalizeCalicoOverlay(snapshot *CNISnapshot) {
	if snapshot.Type != "calico" {
		return
	}
	var modes []string
	if snapshot.Calico.IPIPInterface != "" || containsString(snapshot.Calico.ConfigHints, "ipip") {
		modes = append(modes, "ipip")
	}
	if snapshot.Calico.VXLANInterface != "" || containsString(snapshot.Calico.ConfigHints, "vxlan") {
		modes = append(modes, "vxlan")
	}
	if snapshot.Calico.WireGuardInterface != "" || containsString(snapshot.Calico.ConfigHints, "wireguard") {
		modes = append(modes, "wireguard")
	}
	if len(modes) == 0 {
		modes = append(modes, "none-or-bgp")
	}
	snapshot.Calico.OverlayMode = strings.Join(modes, "+")
	snapshot.OverlayMode = snapshot.Calico.OverlayMode
}

func containsString(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func detectInterfaces() []string {
	entries, err := os.ReadDir("/sys/class/net")
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.Name())
	}
	return out
}
