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
    address-pools:{{range .Networks}}
    - name: {{ .Name}}
      protocol: {{ .Protocol }}
      addresses: {{range $subnet := .Addresses}}
      - {{ $subnet }}
      {{- end}}
    {{- end}}
`)

// PeerDetail holds information about each of the BGP routers that we peer with in MetalLB
type PeerDetail struct {
	IPAddress string `yaml:"peer-address" valid:"_,required"`
	PeerASN   int    `yaml:"peer-asn" valid:"_,required"`
	MyASN     int    `yaml:"my-asn" valid:"_,required"`
}

// AddressPoolDetail holds information about each of the MetalLB address pools
type AddressPoolDetail struct {
	Name      string   `yaml:"name" valid:"_,required"`
	Protocol  string   `yaml:"protocol" valid:"_,required"`
	Addresses []string `yaml:"addresses" valid:"required"`
}

// MetalLBConfigMap holds information needed by the MetalLBConfigMapTemplate
type MetalLBConfigMap struct {
	LeafSwitches  []PeerDetail
	PeerSwitches  []PeerDetail
	SpineSwitches []PeerDetail
	Networks      []AddressPoolDetail
}

// GetMetalLBConfig gathers the information for the metallb config map
func GetMetalLBConfig(v *viper.Viper, networks map[string]*csi.IPV4Network, switches []*csi.ManagementSwitch) MetalLBConfigMap {

	var configStruct MetalLBConfigMap

	var spineSwitchXnames, leafSwitchXnames []string
	var bgpPeers = v.GetString("bgp-peers")

	// Split out switches into spine and leaf lists
	for _, mgmtswitch := range switches {
		if mgmtswitch.SwitchType == "Spine" {
			spineSwitchXnames = append(spineSwitchXnames, mgmtswitch.Xname)
		}
		if mgmtswitch.SwitchType == "Leaf" {
			leafSwitchXnames = append(leafSwitchXnames, mgmtswitch.Xname)
		}
	}

	for name, network := range networks {
		for _, subnet := range network.Subnets {
			// This is a v1.4 HACK related to the supernet.
			if (name == "NMN" || name == "CMN") && subnet.Name == "network_hardware" {
				var tmpPeer PeerDetail
				for _, reservation := range subnet.IPReservations {
					tmpPeer = PeerDetail{}
					tmpPeer.PeerASN = network.PeerASN
					tmpPeer.MyASN = network.MyASN
					tmpPeer.IPAddress = reservation.IPAddress.String()
					for _, switchXname := range spineSwitchXnames {
						if reservation.Comment == switchXname {
							configStruct.SpineSwitches = append(configStruct.SpineSwitches, tmpPeer)
						}
					}
					for _, switchXname := range leafSwitchXnames {
						if reservation.Comment == switchXname {
							configStruct.LeafSwitches = append(configStruct.LeafSwitches, tmpPeer)
						}
					}
				}
			}
			if strings.Contains(subnet.Name, "metallb") {
				tmpAddPool := AddressPoolDetail{}
				tmpAddPool.Name = subnet.MetalLBPoolName
				tmpAddPool.Protocol = "bgp"
				tmpAddPool.Addresses = append(tmpAddPool.Addresses, subnet.CIDR.String())
				configStruct.Networks = append(configStruct.Networks, tmpAddPool)
			}
		}
	}

	configStruct.PeerSwitches = getMetalLBPeerSwitches(bgpPeers, configStruct)

	return configStruct
}

// WriteMetalLBConfigMap creates the yaml configmap
func WriteMetalLBConfigMap(path string, v *viper.Viper, networks map[string]*csi.IPV4Network, switches []*csi.ManagementSwitch) {

	tpl, err := template.New("mtllbconfigmap").Parse(string(MetalLBConfigMapTemplate))
	if err != nil {
		log.Printf("The template failed to render because: %v \n", err)
	}

	configStruct := GetMetalLBConfig(v, networks, switches)

	csiFiles.WriteTemplate(filepath.Join(path, "metallb.yaml"), tpl, configStruct)
}

// getMetalLBPeerSwitches returns a list of switches  that should be used as metallb peers
func getMetalLBPeerSwitches(bgpPeers string, configStruct MetalLBConfigMap) []PeerDetail {

	switchTypeMap := map[string][]PeerDetail{
		"spine": configStruct.SpineSwitches,
		"leaf":  configStruct.LeafSwitches,
	}

	if peerSwitches, ok := switchTypeMap[bgpPeers]; ok {
		if len(peerSwitches) == 0 {
			log.Fatalf("bgp-peers: %s specified but none defined in switch_metadata.csv\n", bgpPeers)
		}
		configStruct.PeerSwitches = append(configStruct.PeerSwitches, peerSwitches...)
	} else {
		log.Fatalf("bgp-peers: unrecognized option: %s\n", bgpPeers)
	}

	return configStruct.PeerSwitches
}
