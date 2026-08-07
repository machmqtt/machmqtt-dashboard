package collector

// TopologyGraph is the graph for the topology page.
type TopologyGraph struct {
	Nodes []TopologyNode `json:"nodes"`
	Links []TopologyLink `json:"links"`
}

type TopologyNode struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Type        string  `json:"type"` // "server", "gateway", "leaf"
	Connections int     `json:"connections"`
	Healthy     bool    `json:"healthy"`
	InMsgsRate  float64 `json:"in_msgs_rate"`
	OutMsgsRate float64 `json:"out_msgs_rate"`
	Cluster     string  `json:"cluster,omitempty"`
}

type TopologyLink struct {
	Source      string  `json:"source"`
	Target      string  `json:"target"`
	Type        string  `json:"type"` // "route", "gateway", "leaf"
	InMsgsRate  float64 `json:"in_msgs_rate"`
	OutMsgsRate float64 `json:"out_msgs_rate"`
}

func buildTopology(snap, prev *Snapshot) *TopologyGraph {
	g := &TopologyGraph{}
	seenNodes := make(map[string]bool)
	seenLinks := make(map[string]bool)

	// Compute time delta for rate calculation.
	var dt float64
	if prev != nil && !prev.Timestamp.IsZero() && !snap.Timestamp.IsZero() {
		dt = snap.Timestamp.Sub(prev.Timestamp).Seconds()
	}
	if dt <= 0 {
		dt = 0
	}

	nameToID := make(map[string]string)
	for id, v := range snap.Varz {
		if v.ServerName != "" {
			nameToID[v.ServerName] = id
		}
	}

	for id, v := range snap.Varz {
		healthy := true
		if h, ok := snap.Health[id]; ok {
			healthy = h.Status == "ok"
		}
		node := TopologyNode{
			ID:          id,
			Name:        v.ServerName,
			Type:        "server",
			Connections: v.Connections,
			Healthy:     healthy,
			Cluster:     v.Cluster.Name,
		}
		if r, ok := snap.Rates[id]; ok {
			node.InMsgsRate = r.InMsgsRate
			node.OutMsgsRate = r.OutMsgsRate
		}
		g.Nodes = append(g.Nodes, node)
		seenNodes[id] = true
	}

	addNode := func(id, name, nodeType string) {
		if seenNodes[id] {
			return
		}
		seenNodes[id] = true
		g.Nodes = append(g.Nodes, TopologyNode{
			ID: id, Name: name, Type: nodeType, Healthy: true,
		})
	}

	// Helper to compute per-link rate from cumulative counters.
	// Look up the same route/leaf in the previous snapshot and compute delta.
	prevRouteMsg := func(serverID, remoteID string) (int64, int64) {
		if prev == nil {
			return 0, 0
		}
		r, ok := prev.Routez[serverID]
		if !ok {
			return 0, 0
		}
		for _, route := range r.Routes {
			if route.RemoteID == remoteID {
				return route.InMsgs, route.OutMsgs
			}
		}
		return 0, 0
	}

	prevLeafMsg := func(serverID string, leafName string) (int64, int64) {
		if prev == nil {
			return 0, 0
		}
		lz, ok := prev.Leafz[serverID]
		if !ok {
			return 0, 0
		}
		for _, l := range lz.Leafs {
			name := l.Name
			if name == "" {
				name = l.IP
			}
			if name == leafName {
				return l.InMsgs, l.OutMsgs
			}
		}
		return 0, 0
	}

	rate := func(cur, prev int64) float64 {
		if dt <= 0 || cur <= prev {
			return 0
		}
		return float64(cur-prev) / dt
	}

	// Route edges.
	for srcID, r := range snap.Routez {
		for _, route := range r.Routes {
			addNode(route.RemoteID, route.RemoteName, "server")

			linkKey := routeLinkKey(srcID, route.RemoteID)
			if seenLinks[linkKey] {
				continue
			}
			seenLinks[linkKey] = true

			prevIn, prevOut := prevRouteMsg(srcID, route.RemoteID)
			g.Links = append(g.Links, TopologyLink{
				Source:      srcID,
				Target:      route.RemoteID,
				Type:        "route",
				InMsgsRate:  rate(route.InMsgs, prevIn),
				OutMsgsRate: rate(route.OutMsgs, prevOut),
			})
		}
	}

	// Gateway edges.
	for srcID, gw := range snap.Gatewayz {
		for gwName := range gw.OutboundGateways {
			targetID := "gw:" + gwName
			addNode(targetID, gwName, "gateway")

			linkKey := srcID + "->" + targetID
			if !seenLinks[linkKey] {
				seenLinks[linkKey] = true
				// Gateway links don't carry msg-rate deltas (no simple prev
				// lookup), so they render with zero rates.
				link := TopologyLink{Source: srcID, Target: targetID, Type: "gateway"}
				g.Links = append(g.Links, link)
			}
		}
	}

	// Leaf edges.
	for srcID, lz := range snap.Leafz {
		for _, leaf := range lz.Leafs {
			leafName := leaf.Name
			if leafName == "" {
				leafName = leaf.IP
			}

			targetID := ""
			if knownID, ok := nameToID[leafName]; ok {
				targetID = knownID
			} else {
				targetID = "leaf:" + leafName
				addNode(targetID, leafName, "leaf")
			}

			linkKey := routeLinkKey(srcID, targetID)
			if !seenLinks[linkKey] {
				seenLinks[linkKey] = true

				prevIn, prevOut := prevLeafMsg(srcID, leafName)
				g.Links = append(g.Links, TopologyLink{
					Source:      srcID,
					Target:      targetID,
					Type:        "leaf",
					InMsgsRate:  rate(leaf.InMsgs, prevIn),
					OutMsgsRate: rate(leaf.OutMsgs, prevOut),
				})
			}
		}
	}

	// MQTT bridge nodes — discovered from connz data.
	// Group bridge connections by (server_id, ip) to create one node per bridge instance.
	type mqttBridgeKey struct{ serverID, ip string }
	mqttBridges := make(map[mqttBridgeKey]*struct {
		totalSubs int
		inMsgs    int64
		outMsgs   int64
		prevIn    int64
		prevOut   int64
		conns     int
	})

	for srvID, connz := range snap.Connz {
		for _, c := range connz.Conns {
			if !isMQTTBridgeConn(c.Name) {
				continue
			}
			ip := resolveBridgeIP(srvID, c.IP, snap)
			key := mqttBridgeKey{serverID: srvID, ip: ip}
			b := mqttBridges[key]
			if b == nil {
				b = &struct {
					totalSubs int
					inMsgs    int64
					outMsgs   int64
					prevIn    int64
					prevOut   int64
					conns     int
				}{}
				mqttBridges[key] = b
			}
			b.conns++
			b.totalSubs += int(c.NumSubs)
			b.inMsgs += c.InMsgs
			b.outMsgs += c.OutMsgs
		}
	}

	// Compute prev totals for rate calculation.
	if prev != nil {
		for srvID, connz := range prev.Connz {
			for _, c := range connz.Conns {
				if !isMQTTBridgeConn(c.Name) {
					continue
				}
				ip := resolveBridgeIP(srvID, c.IP, prev)
				key := mqttBridgeKey{serverID: srvID, ip: ip}
				if b, ok := mqttBridges[key]; ok {
					b.prevIn += c.InMsgs
					b.prevOut += c.OutMsgs
				}
			}
		}
	}

	for key, b := range mqttBridges {
		// Key the node by the server it attaches to as well as its IP. Co-located
		// brokers (several on one host, or an all-loopback demo) share an IP but
		// attach to different servers; keying on IP alone would collapse them into
		// one node. Name it after that server so each broker reads as "the broker
		// on <edge>".
		nodeID := "mqtt:" + key.serverID + ":" + key.ip
		name := "mqtt@" + key.ip
		if v, ok := snap.Varz[key.serverID]; ok && v.ServerName != "" {
			name = "mqtt@" + v.ServerName
		}
		if !seenNodes[nodeID] {
			seenNodes[nodeID] = true
			g.Nodes = append(g.Nodes, TopologyNode{
				ID:          nodeID,
				Name:        name,
				Type:        "mqtt",
				Connections: b.conns,
				Healthy:     true,
				InMsgsRate:  rate(b.inMsgs, b.prevIn),
				OutMsgsRate: rate(b.outMsgs, b.prevOut),
			})
		}

		linkKey := routeLinkKey(key.serverID, nodeID)
		if !seenLinks[linkKey] {
			seenLinks[linkKey] = true
			g.Links = append(g.Links, TopologyLink{
				Source:      key.serverID,
				Target:      nodeID,
				Type:        "mqtt",
				InMsgsRate:  rate(b.inMsgs, b.prevIn),
				OutMsgsRate: rate(b.outMsgs, b.prevOut),
			})
		}
	}

	return g
}

func routeLinkKey(a, b string) string {
	if a < b {
		return a + "<->" + b
	}
	return b + "<->" + a
}
