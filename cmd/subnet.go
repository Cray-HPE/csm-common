/*
Copyright 2020 Hewlett Packard Enterprise Development LP
*/

package cmd

import (
	"fmt"
	"log"
	"net"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v2"
	"stash.us.cray.com/MTL/csi/pkg/ipam"
	"stash.us.cray.com/MTL/csi/pkg/shasta"
)

// subnetCmd represents the subnet command
var subnetCmd = &cobra.Command{
	Use:   "subnet [name]",
	Short: "Build the yaml for a Shasta Subnet",
	Long:  `Build the yaml for a Shasta Subnet`,
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		v := viper.GetViper()
		var n shasta.IPV4Subnet
		err := v.Unmarshal(&n)
		if err != nil {
			log.Fatalf("unable to decode configuration into usable struct, %v \n", err)
		}
		n.Name = args[0]
		_, network, _ := net.ParseCIDR(v.GetString("within"))
		viperSize, _ := strconv.Atoi(v.GetString("size"))
		n.CIDR, _ = ipam.SubnetWithin(*network, viperSize)
		bs, _ := yaml.Marshal(&n)
		fmt.Print(string(bs))
	},
}

func init() {
	rawCmd.AddCommand(subnetCmd)

	subnetCmd.Flags().String("full_name", "", "Long Descriptive Name for the Subnet")
	subnetCmd.Flags().Int("size", 16, "Number of ip addresses in the subnet")
	subnetCmd.Flags().Int16("vlan_id", 0, "Preferred VlanID")
	subnetCmd.Flags().String("comment", "", "Subnet Comment")
	subnetCmd.Flags().IP("gateway", net.IP{}, "Subnet Gateway")
	subnetCmd.Flags().IPNet("within", net.IPNet{}, "Overall IPv4 CIDR for all Provisioning subnets")
}