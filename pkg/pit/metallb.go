/*
Copyright 2021 Hewlett Packard Enterprise Development LP
*/

package pit

import (
	"log"
	"path/filepath"
	"strings"
	"text/template"

	csiFiles "github.com/Cray-HPE/cray-site-init/internal/files"
	"github.com/Cray-HPE/cray-site-init/pkg/csi"
	"github.com/spf13/viper"
)

// MetalLBConfigMapTemplate manages the ConfigMap for MetalLB
var MetalLBConfigMapTemplate = []byte(`
---
apiVersion: v1
kind: ConfigMap
metadata:
  namespace: metallb-system
  name: config
data:
  config: |
    peers:{{range .PeerSwitches}}
    - peer-address: {{ .IPAddress }}
      peer-asn: {{ .PeerASN }}
      my-asn: {{ .MyASN }}
      {{- end}}
    address-pools:{{range $name, $subnet := .Networks}}
    - name: {{$name}}
      protocol: bgp
      addresses:
      - {{ $subnet }}
    {{- end}}
`)

// PeerDetail holds information about each of the BGP routers that we peer with in MetalLB
type PeerDetail struct {
	Network   string
	IPAddress string
	PeerASN   string
	MyASN     string
}

// MetalLBConfigMap holds information needed by the MetalLBConfigMapTemplate
type MetalLBConfigMap struct {
	AggSwitches   []PeerDetail
	PeerSwitches  []PeerDetail
	SpineSwitches []PeerDetail
	Networks      map[string]string
}

// WriteMetalLBConfigMap creates the yaml configmap
func WriteMetalLBConfigMap(path string, v *viper.Viper, networks map[string]*csi.IPV4Network, switches []*csi.ManagementSwitch) {

	// this lookup table should be redundant in the future
	// when we can better hint which pool an endpoint should pull from
	var metalLBLookupTable = map[string]string{
		"nmn_metallb_address_pool": "node-management",
		"hmn_metallb_address_pool": "hardware-management",
		"hsn_metallb_address_pool": "high-speed",
		// "can_metallb_address_pool": "customer-access",
		// "can_metallb_static_pool":  "customer-access-static",
	}

	tpl, err := template.New("mtllbconfigmap").Parse(string(MetalLBConfigMapTemplate))
	if err != nil {
		log.Printf("The template failed to render because: %v \n", err)
	}
	var configStruct MetalLBConfigMap
	configStruct.Networks = make(map[string]string)

	var spineSwitchXnames, aggSwitchXnames []string
	var bgpPeers = v.GetString("bgp-peers")

	for _, mgmtswitch := range switches {
		if mgmtswitch.SwitchType == "Spine" {
			spineSwitchXnames = append(spineSwitchXnames, mgmtswitch.Xname)
		}
		if mgmtswitch.SwitchType == "Aggregation" {
			aggSwitchXnames = append(aggSwitchXnames, mgmtswitch.Xname)
		}
	}

	for name, network := range networks {
		for _, subnet := range network.Subnets {
			// This is a v1.4 HACK related to the supernet.
			if name == "NMN" && subnet.Name == "network_hardware" {
				var tmpPeer PeerDetail
				for _, reservation := range subnet.IPReservations {
					tmpPeer = PeerDetail{}
					tmpPeer.Network = name
					tmpPeer.PeerASN = v.GetString("bgp-asn")
					tmpPeer.MyASN = v.GetString("bgp-asn")
					tmpPeer.IPAddress = reservation.IPAddress.String()
					for _, switchXname := range spineSwitchXnames {
						if reservation.Comment == switchXname {
							configStruct.SpineSwitches = append(configStruct.SpineSwitches, tmpPeer)
						}
					}
					for _, switchXname := range aggSwitchXnames {
						if reservation.Comment == switchXname {
							configStruct.AggSwitches = append(configStruct.AggSwitches, tmpPeer)
						}
					}
				}
			}
			if name == "CMN" && subnet.Name == "bootstrap_dhcp" {
				var tmpPeer PeerDetail
				for _, reservation := range subnet.IPReservations {
					if strings.Contains(reservation.Name, "cmn-switch") {
						tmpPeer = PeerDetail{}
						tmpPeer.Network = name
						tmpPeer.PeerASN = v.GetString("bgp-asn")
						tmpPeer.MyASN = v.GetString("bgp-cmn-asn")
						tmpPeer.IPAddress = reservation.IPAddress.String()
						if bgpPeers == "spine" {
							configStruct.SpineSwitches = append(configStruct.SpineSwitches, tmpPeer)
						} else if bgpPeers == "aggregation" {
							configStruct.AggSwitches = append(configStruct.AggSwitches, tmpPeer)
						} else {
							log.Fatalf("bgp-peers: unrecognized option: %s\n", bgpPeers)
						}
					}
				}
			}
			if strings.Contains(subnet.Name, "metallb") {
				if val, ok := metalLBLookupTable[subnet.Name]; ok {
					configStruct.Networks[val] = subnet.CIDR.String()
				}
			}
			configStruct.Networks["customer-management-static"] = v.GetString("cmn-static-pool")
			configStruct.Networks["customer-management"] = v.GetString("cmn-dynamic-pool")
			if v.GetString("can-static-pool") != "" {
				configStruct.Networks["customer-access-static"] = v.GetString("can-static-pool")
			}
			if v.GetString("can-dynamic-pool") != "" {
				configStruct.Networks["customer-access"] = v.GetString("can-dynamic-pool")
			}
			if v.GetString("chn-static-pool") != "" {
				configStruct.Networks["customer-high-speed-static"] = v.GetString("chn-static-pool")
			}
			if v.GetString("chn-dynamic-pool") != "" {
				configStruct.Networks["customer-high-speed"] = v.GetString("chn-dynamic-pool")
			}
		}
	}

	configStruct.PeerSwitches = getMetalLBPeerSwitches(bgpPeers, configStruct)
	csiFiles.WriteTemplate(filepath.Join(path, "metallb.yaml"), tpl, configStruct)
}

// getMetalLBPeerSwitches returns a list of switches  that should be used as metallb peers
func getMetalLBPeerSwitches(bgpPeers string, configStruct MetalLBConfigMap) []PeerDetail {

	switchTypeMap := map[string][]PeerDetail{
		"spine":       configStruct.SpineSwitches,
		"aggregation": configStruct.AggSwitches,
	}

	if peerSwitches, ok := switchTypeMap[bgpPeers]; ok {
		if len(peerSwitches) == 0 {
			log.Fatalf("bgp-peers: %s specified but none defined in switch_metadata.csv\n", bgpPeers)
		}
		for _, switchDetail := range peerSwitches {
			configStruct.PeerSwitches = append(configStruct.PeerSwitches, switchDetail)
		}
	} else {
		log.Fatalf("bgp-peers: unrecognized option: %s\n", bgpPeers)
	}

	return configStruct.PeerSwitches
}
