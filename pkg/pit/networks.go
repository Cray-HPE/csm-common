/*
Copyright 2021 Hewlett Packard Enterprise Development LP
*/

package pit

import (
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/spf13/viper"

	csiFiles "stash.us.cray.com/MTL/csi/internal/files"
	"stash.us.cray.com/MTL/csi/pkg/csi"
)

// WriteNetworkFiles persistes our network configuration to disk in a directory of yaml files
func WriteNetworkFiles(basepath string, networks map[string]*csi.IPV4Network) {
	for k, v := range networks {
		csiFiles.WriteYAMLConfig(filepath.Join(basepath, fmt.Sprintf("networks/%v.yaml", k)), v)
	}
}

// WriteCPTNetworkConfig writes the Network Configuration details for the installation node  (PIT)
func WriteCPTNetworkConfig(path string, v *viper.Viper, ncn csi.LogicalNCN, shastaNetworks map[string]*csi.IPV4Network) error {
	type Route struct {
		CIDR    net.IP
		Mask    net.IP
		Gateway net.IP
	}
	var bond0Net csi.NCNNetwork
	for _, network := range ncn.Networks {
		if network.NetworkName == "MTL" {
			bond0Net = network
		}
	}
	_, metalNet, _ := net.ParseCIDR(shastaNetworks["NMNLB"].CIDR)
	nmnNetNet, _ := shastaNetworks["NMN"].LookUpSubnet("network_hardware")

	metalLBRoute := Route{
		CIDR:    metalNet.IP,
		Mask:    net.IP(metalNet.Mask),
		Gateway: nmnNetNet.Gateway,
	}
	bond0Struct := struct {
		Bond0 string
		Bond1 string
		Mask  string
		CIDR  string
	}{
		Bond0: strings.Split(v.GetString("install-ncn-bond-members"), ",")[0],
		Bond1: strings.Split(v.GetString("install-ncn-bond-members"), ",")[1],
		Mask:  bond0Net.Mask,
		CIDR:  bond0Net.CIDR,
	}
	csiFiles.WriteTemplate(filepath.Join(path, "ifcfg-bond0"), template.Must(template.New("bond0").Parse(string(Bond0ConfigTemplate))), bond0Struct)
	siteNetDef := strings.Split(v.GetString("site-ip"), "/")
	lan0struct := struct {
		Nic, IP, IPPrefix string
	}{
		v.GetString("site-nic"),
		v.GetString("site-ip"),
		siteNetDef[1],
	}
	lan0RouteStruct := struct {
		CIDR    string
		Mask    string
		Gateway string
	}{"default", "-", v.GetString("site-gw")}

	csiFiles.WriteTemplate(filepath.Join(path, "ifcfg-lan0"), template.Must(template.New("lan0").Parse(string(Lan0ConfigTemplate))), lan0struct)
	lan0sysconfig := struct {
		SiteDNS string
	}{
		v.GetString("site-dns"),
	}
	csiFiles.WriteTemplate(filepath.Join(path, "config"), template.Must(template.New("netcofig").Parse(string(sysconfigNetworkConfigTemplate))), lan0sysconfig)
	csiFiles.WriteTemplate(filepath.Join(path, "ifroute-lan0"), template.Must(template.New("vlan").Parse(string(VlanRouteTemplate))), []interface{}{lan0RouteStruct})
	for _, network := range ncn.Networks {
		if stringInSlice(network.NetworkName, csi.ValidNetNames) {
			if network.Vlan != 0 {
				csiFiles.WriteTemplate(filepath.Join(path, fmt.Sprintf("ifcfg-vlan%03d", network.Vlan)), template.Must(template.New("vlan").Parse(string(VlanConfigTemplate))), network)
			}
			if network.NetworkName == "NMN" {
				csiFiles.WriteTemplate(filepath.Join(path, fmt.Sprintf("ifroute-vlan%03d", network.Vlan)), template.Must(template.New("vlan").Parse(string(VlanRouteTemplate))), []Route{metalLBRoute})
			}
		}
	}
	return nil
}

// VlanConfigTemplate is the text/template to bootstrap the install cd
var VlanConfigTemplate = []byte(`
NAME='{{.FullName}}'

# Set static IP (becomes "preferred" if dhcp is enabled)
BOOTPROTO='static'
IPADDR='{{.CIDR}}'    # i.e. '192.168.80.1/20'
PREFIXLEN='{{.Mask}}' # i.e. '20'

# CHANGE AT OWN RISK:
ETHERDEVICE='bond0'

# DO NOT CHANGE THESE:
VLAN_PROTOCOL='ieee802-1Q'
ONBOOT='yes'
STARTMODE='auto'
`)

// VlanRouteTemplate allows us to add static routes to the vlan(s) on the PIT node
var VlanRouteTemplate = []byte(`
{{- range . -}}
{{.CIDR}} {{.Gateway}} {{.Mask}} -
{{ end -}}
`)

// Bond0ConfigTemplate is the text/template for setting up the bond on the install NCN
var Bond0ConfigTemplate = []byte(`
NAME='Internal Interface'# Select the NIC(s) for access to the CRAY.

# Select the NIC(s) for access.
BONDING_SLAVE0='{{.Bond0}}'
BONDING_SLAVE1='{{.Bond1}}'

# Set static IP (becomes "preferred" if dhcp is enabled)
BOOTPROTO='static'
IPADDR='{{.CIDR}}'    # i.e. '192.168.64.1/20'
PREFIXLEN='{{.Mask}}' # i.e. '20'

# CHANGE AT OWN RISK:
BONDING_MODULE_OPTS='mode=802.3ad miimon=100 lacp_rate=fast xmit_hash_policy=layer2+3'# DO NOT CHANGE THESE:

# DO NOT CHANGE THESE:
ONBOOT='yes'
STARTMODE='manual'
BONDING_MASTER='yes'
`)

// https://stash.us.cray.com/projects/MTL/repos/shasta-pre-install-toolkit/browse/suse/x86_64/shasta-pre-install-toolkit-sle15sp2/root

// Lan0ConfigTemplate is the text/template for handling the external site link
var Lan0ConfigTemplate = []byte(`
NAME='External Site-Link'

# Select the NIC(s) for direct, external access.
BRIDGE_PORTS='{{.Nic}}'

# Set static IP (becomes "preferred" if dhcp is enabled)
# NOTE: IPADDR's route will override DHCPs.
BOOTPROTO='static'
IPADDR='{{.IP}}'    # i.e. 10.100.10.1/24
PREFIXLEN='{{.IPPrefix}}' # i.e. 24

# DO NOT CHANGE THESE:
ONBOOT='yes'
STARTMODE='auto'
BRIDGE='yes'
BRIDGE_STP='no'
`)

var sysconfigNetworkConfigTemplate = []byte(`
# Generated by CSI as part of the installation planning
AUTO6_WAIT_AT_BOOT=""
AUTO6_UPDATE=""
LINK_REQUIRED="auto"
WICKED_DEBUG=""
WICKED_LOG_LEVEL=""
CHECK_DUPLICATE_IP="yes"
SEND_GRATUITOUS_ARP="auto"
DEBUG="no"
WAIT_FOR_INTERFACES="30"
FIREWALL="yes"
NM_ONLINE_TIMEOUT="30"
NETCONFIG_MODULES_ORDER="dns-resolver dns-bind dns-dnsmasq nis ntp-runtime"
NETCONFIG_VERBOSE="no"
NETCONFIG_FORCE_REPLACE="no"
NETCONFIG_DNS_POLICY="auto"
NETCONFIG_DNS_FORWARDER="dnsmasq"
NETCONFIG_DNS_FORWARDER_FALLBACK="yes"
NETCONFIG_DNS_STATIC_SEARCHLIST="nmn hmn"
NETCONFIG_DNS_STATIC_SERVERS="{{.SiteDNS}}"
NETCONFIG_DNS_RANKING="auto"
NETCONFIG_DNS_RESOLVER_OPTIONS=""
NETCONFIG_DNS_RESOLVER_SORTLIST=""
NETCONFIG_NTP_POLICY="auto"
NETCONFIG_NTP_STATIC_SERVERS=""
NETCONFIG_NIS_POLICY="auto"
NETCONFIG_NIS_SETDOMAINNAME="yes"
NETCONFIG_NIS_STATIC_DOMAIN=""
NETCONFIG_NIS_STATIC_SERVERS=""
WIRELESS_REGULATORY_DOMAIN=''
`)