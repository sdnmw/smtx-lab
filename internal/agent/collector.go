package agent

import (
	"context"
	"fmt"
	"time"
)

type Collector struct {
	NodeName       string
	CNIConfigDirs  []string
	ProcRoot       string
	CommandTimeout time.Duration
	MaxOutputBytes int
}

func (c Collector) Collect(ctx context.Context, req SnapshotRequest) Snapshot {
	snapshot := Snapshot{
		NodeName: c.NodeName,
		Time:     time.Now().UTC(),
	}
	if req.CollectCNI {
		cni, err := c.DetectCNI()
		if err != nil {
			snapshot.Errors = append(snapshot.Errors, fmt.Sprintf("detect cni: %v", err))
		}
		snapshot.CNI = cni
	}
	if req.CollectIptables {
		iptables, err := c.CollectIptables(ctx, req.ChainAllowlist)
		if err != nil {
			snapshot.Errors = append(snapshot.Errors, fmt.Sprintf("collect iptables: %v", err))
		}
		snapshot.Iptables = iptables
	}
	if req.CollectIPVS {
		ipvs, err := c.CollectIPVS()
		if err != nil {
			snapshot.Errors = append(snapshot.Errors, fmt.Sprintf("collect ipvs: %v", err))
		}
		snapshot.IPVS = ipvs
	}
	if req.CollectConntrack {
		conntrack, err := c.CollectConntrack(ctx, req.ConntrackFilter)
		if err != nil {
			snapshot.Errors = append(snapshot.Errors, fmt.Sprintf("collect conntrack: %v", err))
		}
		snapshot.Conntrack = conntrack
	}
	return snapshot
}
