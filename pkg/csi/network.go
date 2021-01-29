/*
Copyright 2020 Hewlett Packard Enterprise Development LP
*/

package csi

import (
	"fmt"
	"log"
	"net"
	"strings"

	sls_common "stash.us.cray.com/HMS/hms-sls/pkg/sls-common"
	"stash.us.cray.com/MTL/csi/pkg/ipam"
)

// IPV4Network is a type for managing IPv4 Networks
type IPV4Network struct {
	FullName  string                 `yaml:"full_name"`
	CIDR      string                 `yaml:"cidr"`
	Subnets   []*IPV4Subnet          `yaml:"subnets"`
	Name      string                 `yaml:"name"`
	VlanRange []int16                `yaml:"vlan_range"`
	MTU       int16                  `yaml:"mtu"`
	NetType   sls_common.NetworkType `yaml:"type"`
	Comment   string                 `yaml:"comment"`
}

// IPV4Subnet is a type for managing IPv4 Subnets
type IPV4Subnet struct {
	FullName       string          `yaml:"full_name" form:"full_name" mapstructure:"full_name"`
	CIDR           net.IPNet       `yaml:"cidr"`
	IPReservations []IPReservation `yaml:"ip_reservations"`
	Name           string          `yaml:"name" form:"name" mapstructure:"name"`
	NetName        string          `yaml:"net-name"`
	VlanID         int16           `yaml:"vlan_id" form:"vlan_id" mapstructure:"vlan_id"`
	Comment        string          `yaml:"comment"`
	Gateway        net.IP          `yaml:"gateway"`
	SupernetRouter net.IP          `yaml:"_"`
	DNSServer      net.IP          `yaml:"dns_server"`
	DHCPStart      net.IP          `yaml:"iprange-start"`
	DHCPEnd        net.IP          `yaml:"iprange-end"`
}

// IPReservation is a type for managing IP Reservations
type IPReservation struct {
	IPAddress net.IP   `yaml:"ip_address"`
	Name      string   `yaml:"name"`
	Comment   string   `yaml:"comment"`
	Aliases   []string `yaml:"aliases"`
}

// GenSubnets subdivides a network into a set of subnets
func (iNet *IPV4Network) GenSubnets(cabinetDetails []CabinetGroupDetail, mask net.IPMask, cabinetType string) error {
	log.Printf("Generating Subnets for %s\ncabinetType: %v,\n", iNet.Name, cabinetType)
	_, myNet, _ := net.ParseCIDR(iNet.CIDR)
	mySubnets := iNet.AllocatedSubnets()
	myIPv4Subnets := iNet.Subnets

	for _, cabinetDetail := range cabinetDetails {
		if cabinetType == cabinetDetail.Kind {
			log.Println("Dealing with CabinetDetail: ", cabinetDetail)

			for j, i := range cabinetDetail.CabinetDetails {
				newSubnet, err := ipam.Free(*myNet, mask, mySubnets)
				mySubnets = append(mySubnets, newSubnet)
				if err != nil {
					log.Printf("Gensubnets couldn't add subnet because %v \n", err)
					return err
				}
				var tmpVlanID = i.VlanID
				if tmpVlanID == 0 {
					tmpVlanID = int16(j) + iNet.VlanRange[0]
				}
				tempSubnet := IPV4Subnet{
					CIDR:    newSubnet,
					Name:    fmt.Sprintf("cabinet_%d", i.ID),
					Gateway: ipam.Add(newSubnet.IP, 1),
					VlanID:  tmpVlanID,
				}
				// Bump the DHCP Start IP past the gateway
				tempSubnet.DHCPStart = ipam.Add(tempSubnet.CIDR.IP, len(tempSubnet.IPReservations)+2)
				tempSubnet.DHCPEnd = ipam.Add(ipam.Broadcast(tempSubnet.CIDR), -1)
				myIPv4Subnets = append(myIPv4Subnets, &tempSubnet)
			}
		}
	}
	iNet.Subnets = myIPv4Subnets
	return nil
}

// AllocatedSubnets returns a list of the allocated subnets
func (iNet IPV4Network) AllocatedSubnets() []net.IPNet {
	var myNets []net.IPNet
	for _, v := range iNet.Subnets {
		myNets = append(myNets, v.CIDR)
	}
	return myNets
}

// AddSubnetbyCIDR allocates a new subnet
func (iNet *IPV4Network) AddSubnetbyCIDR(desiredNet net.IPNet, name string, vlanID int16) (*IPV4Subnet, error) {
	_, myNet, _ := net.ParseCIDR(iNet.CIDR)
	if ipam.Contains(*myNet, desiredNet) {
		iNet.Subnets = append(iNet.Subnets, &IPV4Subnet{
			CIDR:    desiredNet,
			Name:    name,
			Gateway: ipam.Add(desiredNet.IP, 1),
			VlanID:  vlanID,
		})
		return iNet.Subnets[len(iNet.Subnets)-1], nil
	}
	return &IPV4Subnet{}, fmt.Errorf("subnet %v is not part of %v", desiredNet.String(), myNet.String())
}

// AddSubnet allocates a new subnet
func (iNet *IPV4Network) AddSubnet(mask net.IPMask, name string, vlanID int16) (*IPV4Subnet, error) {
	var tempSubnet IPV4Subnet
	_, myNet, _ := net.ParseCIDR(iNet.CIDR)
	newSubnet, err := ipam.Free(*myNet, mask, iNet.AllocatedSubnets())
	if err != nil {
		return &tempSubnet, err
	}
	iNet.Subnets = append(iNet.Subnets, &IPV4Subnet{
		CIDR:    newSubnet,
		Name:    name,
		NetName: iNet.Name,
		Gateway: ipam.Add(newSubnet.IP, 1),
		VlanID:  vlanID,
	})
	return iNet.Subnets[len(iNet.Subnets)-1], nil
}

// AddBiggestSubnet allocates the largest subnet possible within the requested network and mask
func (iNet *IPV4Network) AddBiggestSubnet(mask net.IPMask, name string, vlanID int16) (*IPV4Subnet, error) {
	// Try for the largest available and go smaller if needed
	maskSize, _ := mask.Size() // the second output of this function is 32 for ipv4 or 64 for ipv6
	for i := maskSize; i < 29; i++ {
		// log.Printf("Trying to find room for a /%d mask in %v \n", i, iNet.Name)
		newSubnet, err := iNet.AddSubnet(net.CIDRMask(i, 32), name, vlanID)
		if err == nil {
			return newSubnet, nil
		}
	}
	return &IPV4Subnet{}, fmt.Errorf("no room for %v subnet within %v (tried from /%d to /29)", name, iNet.Name, maskSize)
}

// LookUpSubnet returns a subnet by name
func (iNet *IPV4Network) LookUpSubnet(name string) (*IPV4Subnet, error) {
	var found []*IPV4Subnet
	if len(iNet.Subnets) == 0 {
		return &IPV4Subnet{}, fmt.Errorf("subnet not found \"%v\"", name)
	}
	for _, v := range iNet.Subnets {
		if v.Name == name {
			found = append(found, v)
		}
	}
	if len(found) == 1 {
		return found[0], nil
	}
	if len(found) > 1 {
		log.Printf("Found %v subnets named %v in the %v network instead of just one \n", len(found), name, iNet.Name)
		return found[0], fmt.Errorf("found %v subnets instead of just one", len(found))
	}
	return &IPV4Subnet{}, fmt.Errorf("subnet not found \"%v\"", name)
}

// SubnetbyName Return a copy of the subnet by name or a blank subnet if it doesn't exists
func (iNet IPV4Network) SubnetbyName(name string) IPV4Subnet {
	for _, v := range iNet.Subnets {
		if strings.EqualFold(v.Name, name) {
			return *v
		}
	}
	return IPV4Subnet{}
}

// ReserveNetMgmtIPs reserves (n) IP addresses for management networking equipment
func (iSubnet *IPV4Subnet) ReserveNetMgmtIPs(spines []string, leafs []string, aggs []string, cdus []string, additional int) {
	for i := 0; i < len(spines); i++ {
		name := fmt.Sprintf("sw-spine-%03d", i+1)
		iSubnet.AddReservation(name, spines[i])
	}
	for i := 0; i < len(leafs); i++ {
		name := fmt.Sprintf("sw-leaf-%03d", i+1)
		iSubnet.AddReservation(name, leafs[i])
	}
	for i := 0; i < len(aggs); i++ {
		name := fmt.Sprintf("sw-agg-%03d", i+1)
		iSubnet.AddReservation(name, aggs[i])
	}
	for i := 0; i < len(cdus); i++ {
		name := fmt.Sprintf("sw-cdu-%03d", i+1)
		iSubnet.AddReservation(name, cdus[i])
	}
	for i := 0; i < additional; i++ {
		name := fmt.Sprintf("mgmt-net-stub-%03d", i+1)
		iSubnet.AddReservation(name, "")
	}
}

// ReservedIPs returns a list of IPs already reserved within the subnet
func (iSubnet *IPV4Subnet) ReservedIPs() []net.IP {
	var addresses []net.IP
	for _, v := range iSubnet.IPReservations {
		addresses = append(addresses, v.IPAddress)
	}
	return addresses
}

// ReservationsByName presents the IPReservations in a map by name
func (iSubnet *IPV4Subnet) ReservationsByName() map[string]IPReservation {
	reservations := make(map[string]IPReservation)
	for _, v := range iSubnet.IPReservations {
		reservations[v.Name] = v
	}
	return reservations
}

// LookupReservation searches the subnet for an IPReservation that matches the name provided
func (iSubnet *IPV4Subnet) LookupReservation(resName string) IPReservation {
	for _, v := range iSubnet.IPReservations {
		if resName == v.Name {
			return v
		}
	}
	return IPReservation{}
}

// TotalIPAddresses returns the number of ip addresses in a subnet see UsableHostAddresses
func (iSubnet *IPV4Subnet) TotalIPAddresses() int {
	maskSize, _ := iSubnet.CIDR.Mask.Size()
	return 2 << uint(31-maskSize)
}

// UsableHostAddresses returns the number of usable ip addresses in a subnet
func (iSubnet *IPV4Subnet) UsableHostAddresses() int {
	maskSize, _ := iSubnet.CIDR.Mask.Size()
	if maskSize == 32 {
		return 1
	} else if maskSize == 31 {
		return 2
	}
	return (iSubnet.TotalIPAddresses() - 2)
}

// UpdateDHCPRange resets the DHCPStart to exclude all IPReservations
func (iSubnet *IPV4Subnet) UpdateDHCPRange(applySupernetHack bool) {

	// log.Printf("Before adjusting the DHCP entries, CIDR is %v and Broadcast is %v\n ", iSubnet.CIDR, ipam.Broadcast(iSubnet.CIDR))
	myReservedIPs := iSubnet.ReservedIPs()
	if len(myReservedIPs) > iSubnet.UsableHostAddresses() {
		log.Fatalf("Could not create %s subnet in %s.  There are %d reservations and only %d usable ip addresses in the subnet %v.", iSubnet.FullName, iSubnet.NetName, len(myReservedIPs), iSubnet.UsableHostAddresses(), iSubnet.CIDR.String())
	}
	// log.Printf("Floor is %v and Broadcast is %v. There are %v reservations with room for %d ips", iSubnet.CIDR.IP, ipam.Broadcast(iSubnet.CIDR), len(myReservedIPs), iSubnet.UsableHostAddresses())
	ip := ipam.Add(iSubnet.CIDR.IP, len(myReservedIPs)+2)
	iSubnet.DHCPStart = ip
	// log.Printf("Inside UpdateDHCPRange and ip = %v which is at %v in list\n", ip, netIPInSlice(ip, myReservedIPs))
	for ipam.NetIPInSlice(ip, myReservedIPs) > 0 {
		iSubnet.DHCPStart = ipam.Add(ip, 2)
		//log.Printf("Dealing with DHCPStart as %v \n", iSubnet.DHCPStart)
		ip = ipam.Add(ip, 1)
	}
	if applySupernetHack {
		iSubnet.DHCPEnd = ipam.Add(iSubnet.DHCPStart, 200) // In this strange world, we can't rely on the broadcast number to be accurate
	} else {
		iSubnet.DHCPEnd = ipam.Add(ipam.Broadcast(iSubnet.CIDR), -1)
	}
	// log.Printf("After adjusting the DHCP entries, we have %v and %v\n ", iSubnet.DHCPStart, iSubnet.DHCPEnd)
}

// AddReservationWithPin adds a new IPv4 reservation to the subnet with the last octet pinned
func (iSubnet *IPV4Subnet) AddReservationWithPin(name, comment string, pin uint8) *IPReservation {
	// Grab the "floor" of the subnet and alter the last byte to match the pinned byte
	// modulo 4/16 bit ip addresses
	// Worth noting that I could not seem to do this by copying the IP from the struct into a new
	// net.IP struct and moddifying only the last byte.  I suspected complier error, but as every
	// good programmer knows, it's probably not a compiler error and the time to debug the compiler
	// is not *NOW*
	newIP := make(net.IP, 4)
	if len(iSubnet.CIDR.IP) == 4 {
		newIP[0] = iSubnet.CIDR.IP[0]
		newIP[1] = iSubnet.CIDR.IP[1]
		newIP[2] = iSubnet.CIDR.IP[2]
		newIP[3] = pin
	}
	if len(iSubnet.CIDR.IP) == 16 {
		newIP[0] = iSubnet.CIDR.IP[12]
		newIP[1] = iSubnet.CIDR.IP[13]
		newIP[2] = iSubnet.CIDR.IP[14]
		newIP[3] = pin
	}
	iSubnet.IPReservations = append(iSubnet.IPReservations, IPReservation{
		IPAddress: newIP,
		Name:      name,
		Comment:   comment,
		Aliases:   strings.Split(comment, ","),
	})
	return &iSubnet.IPReservations[len(iSubnet.IPReservations)-1]
}

// AddReservationAlias adds an alias to a reservation if it doesn't already exist
func (iReserv *IPReservation) AddReservationAlias(alias string) {
	if !stringInSlice(alias, iReserv.Aliases) {
		iReserv.Aliases = append(iReserv.Aliases, alias)
	}
}

// AddReservation adds a new IP reservation to the subnet
func (iSubnet *IPV4Subnet) AddReservation(name, comment string) *IPReservation {
	myReservedIPs := iSubnet.ReservedIPs()
	// Commenting out this section because the supernet configuration we're using will trigger this all the time and it shouldn't be an error
	// floor := iSubnet.CIDR.IP.Mask(iSubnet.CIDR.Mask)
	// if !floor.Equal(iSubnet.CIDR.IP) {
	// 	log.Printf("VERY BAD - In reservation. CIDR.IP = %v and floor is %v", iSubnet.CIDR.IP.String(), floor)
	// }
	// Start counting from the bottom knowing the gateway is on the bottom
	tempIP := ipam.Add(iSubnet.CIDR.IP, 2)
	for {
		for _, v := range myReservedIPs {
			if tempIP.Equal(v) {
				tempIP = ipam.Add(tempIP, 1)
			}
		}
		iSubnet.IPReservations = append(iSubnet.IPReservations, IPReservation{
			IPAddress: tempIP,
			Name:      name,
			Comment:   comment,
		})
		return &iSubnet.IPReservations[len(iSubnet.IPReservations)-1]
	}

}
