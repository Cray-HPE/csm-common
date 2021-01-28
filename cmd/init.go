/*
Copyright 2020 Hewlett Packard Enterprise Development LP
*/

package cmd

import (
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	shcd_parser "stash.us.cray.com/HMS/hms-shcd-parser/pkg/shcd-parser"
	sls_common "stash.us.cray.com/HMS/hms-sls/pkg/sls-common"
	"stash.us.cray.com/MTL/csi/internal/files"
	csiFiles "stash.us.cray.com/MTL/csi/internal/files"
	"stash.us.cray.com/MTL/csi/pkg/ipam"
	"stash.us.cray.com/MTL/csi/pkg/shasta"
	"stash.us.cray.com/MTL/csi/pkg/version"
)

// initCmd represents the init command
var initCmd = &cobra.Command{
	Use:   "init",
	Short: "init generates the directory structure for a new system rooted in a directory matching the system-name argument",
	Long: `init generates a scaffolding the Shasta 1.4 configuration payload.  It is based on several input files:
	1. The hmn_connections.json which describes the cabling for the BMCs on the NCNs
	2. The ncn_metadata.csv file documents the MAC addresses of the NCNs to be used in this installation
	   NCN xname,NCN Role,NCN Subrole,BMC MAC,BMC Switch Port,NMN MAC,NMN Switch Port
	3. The switch_metadata.csv file which documents the Xname, Brand, Type, and Model of each switch.  Types are CDU, Leaf, Aggregation, and Spine 
	   Switch Xname,Type,Brand,Model
	
	** NB ** 
	For systems that use non-sequential cabinet id numbers, an additional mapping file is necessary and must be indicated
	with the --cabinets-yaml flag.
	** NB **

	** NB **
	For additional control of the application node identification durring the SLS Input File generation, an additional config file is necessary
	and must be indicated with the --application-node-config-yaml flag.

	Allows control of the following in the SLS Input File:
	1. System specific prefix for Applications node
	2. Specify HSM Subroles for system specifc application nodes
	3. Specify Application node Aliases  
	** NB **

	In addition, there are many flags to impact the layout of the system.  The defaults are generally fine except for the networking flags.
	`,
	Run: func(cmd *cobra.Command, args []string) {
		var err error
		// Initialize the global viper
		v := viper.GetViper()
		v.SetConfigName("system_config")
		v.AddConfigPath(".")
		// Attempt to read the config file, gracefully ignoring errors
		// caused by a config file not being found. Return an error
		// if we cannot parse the config file.
		if err := v.ReadInConfig(); err != nil {
			// It's okay if there isn't a config file
			if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
				log.Fatalln(err)
			}
		}

		flagErrors := validateFlags()
		if len(flagErrors) > 0 {
			cmd.Usage()
			log.Fatalf(strings.Join(flagErrors, "/n"))
		}

		if len(strings.Split(v.GetString("site-ip"), "/")) != 2 {
			cmd.Usage()
			log.Fatalf("FATAL ERROR: Unable to parse %s as --site-ip.  Must be in the format \"192.168.0.1/24\"", v.GetString("site-ip"))

		}

		// Read and validate our three input files
		hmnRows, logicalNcns, switches, applicationNodeConfig := collectInput(v)

		cabinetDetailList := buildCabinetDetails(v)

		for _, cab := range cabinetDetailList {

			log.Printf("\t%v: %d\n", cab.Kind, len(cab.CabinetIDs))
		}

		// Build a set of networks we can use
		shastaNetworks, err := BuildLiveCDNetworks(v, cabinetDetailList, switches)
		if err != nil {
			log.Panic(err)
		}

		if v.GetBool("supernet") {
			// Once we have validated our networks, go through and replace the gateway and netmask on the
			// uai, dhcp, and network hardware subnets to better support the 1.3 network switch configuration
			// *** This is a HACK ***
			for _, netName := range []string{"NMN", "HMN", "MTL", "CAN"} {
				ApplySupernetHack(shastaNetworks[netName])
			}
		}

		// Use our new networks and our list of logicalNCNs to distribute ips
		shasta.AllocateIps(logicalNcns, shastaNetworks) // This function has no return because it is working with lists of pointers.

		// Now we can finally generate the slsState
		slsState := prepareAndGenerateSLS(cabinetDetailList, shastaNetworks, hmnRows, switches, applicationNodeConfig, v.GetInt("starting-mountain-nid"))
		// SLS can tell us which NCNs match with which Xnames, we need to update the IP Reservations
		slsNcns, err := shasta.ExtractSLSNCNs(&slsState)
		if err != nil {
			log.Panic(err) // This should never happen.  I can't really imagine how it would.
		}

		// Merge the SLS NCN list with the NCN list we got at the beginning
		err = mergeNCNs(logicalNcns, slsNcns)
		if err != nil {
			log.Fatalln(err)
		}

		// Cycle through the main networks and update the reservations, masks and dhcp ranges as necessary
		for _, netName := range [4]string{"NMN", "HMN", "CAN", "MTL"} {
			// Grab the supernet details for use in HACK substitution
			tempSubnet, err := shastaNetworks[netName].LookUpSubnet("bootstrap_dhcp")
			if err != nil {
				log.Panic(err)
			}
			// Loop the reservations and update the NCN reservations with hostnames
			// we likely didn't have when we registered the resevation
			updateReservations(tempSubnet, logicalNcns)

			tempSubnet.UpdateDHCPRange(v.GetBool("supernet"))
			// We expect a bootstrap_dhcp in every net, but uai_macvlan is only in
			// the NMN range for today
			if netName == "NMN" {
				tempSubnet, err = shastaNetworks[netName].LookUpSubnet("uai_macvlan")
				if err != nil {
					log.Panic(err)
				}
				updateReservations(tempSubnet, logicalNcns)
			}

		}

		// Update the SLSState with the updated network information
		_, slsState.Networks = prepareNetworkSLS(shastaNetworks)

		// Switch from a list of pointers to a list of things before we write it out
		var ncns []shasta.LogicalNCN
		for _, ncn := range logicalNcns {
			ncns = append(ncns, *ncn)
		}
		globals, err := shasta.MakeBasecampGlobals(v, ncns, shastaNetworks, "NMN", "bootstrap_dhcp", v.GetString("install-ncn"))
		if err != nil {
			log.Fatalln("unable to generate basecamp globals: ", err)
		}
		writeOutput(v, shastaNetworks, slsState, ncns, switches, globals)

		// Gather SLS information for summary
		slsMountainCabinets := shasta.GetSLSCabinets(slsState, sls_common.ClassMountain)
		slsHillCabinets := shasta.GetSLSCabinets(slsState, sls_common.ClassHill)
		slsRiverCabinets := shasta.GetSLSCabinets(slsState, sls_common.ClassRiver)

		// Print Summary
		fmt.Printf("\n\n===== %v Installation Summary =====\n\n", v.GetString("system-name"))
		fmt.Printf("Installation Node: %v\n", v.GetString("install-ncn"))
		fmt.Printf("Customer Access: %v GW: %v\n", v.GetString("can-cidr"), v.GetString("can-gateway"))
		fmt.Printf("\tUpstream NTP: %v\n", v.GetString("ntp-pool"))
		fmt.Printf("\tUpstream DNS: %v\n", v.GetString("ipv4-resolvers"))
		fmt.Println("Networking")
		for netName, tempNet := range shastaNetworks {
			fmt.Printf("\t* %v %v with %d subnets \n", tempNet.FullName, tempNet.CIDR, len(tempNet.Subnets))
			if v.GetBool("supernet") && stringInSlice(netName, []string{"NMN", "HMN", "MTL", "CAN"}) {
				_, superNet, _ := net.ParseCIDR(shastaNetworks[netName].CIDR)
				maskSize, _ := superNet.Mask.Size()
				fmt.Printf("\t\tSupernet enabled - Using /%v as netmask and %v as Gateway\n", maskSize, ipam.Add(superNet.IP, 1))
			}
		}
		fmt.Printf("System Information\n")
		fmt.Printf("\tNCNs: %v\n", len(ncns))
		fmt.Printf("\tMountain Compute Cabinets: %v\n", len(slsMountainCabinets))
		fmt.Printf("\tHill Compute Cabinets: %v\n", len(slsHillCabinets))
		fmt.Printf("\tRiver Compute Cabinets: %v\n", len(slsRiverCabinets))
		fmt.Printf("CSI Version Information\n\t%s\n\t%s\n\n", version.Get().GitCommit, version.Get())
	},
}

func init() {
	configCmd.AddCommand(initCmd)

	// Flags with defaults for initializing a configuration

	// System Configuration Flags based on previous system_config.yml and networks_derived.yml
	initCmd.Flags().String("system-name", "sn-2024", "Name of the System")
	initCmd.Flags().String("site-domain", "dev.cray.com", "Site Domain Name")
	// initCmd.Flags().String("internal-domain", "unicos.shasta", "Internal Domain Name")
	initCmd.Flags().String("ntp-pool", "time.nist.gov", "Hostname for Upstream NTP Pool")
	initCmd.Flags().String("ipv4-resolvers", "8.8.8.8, 9.9.9.9", "List of IP Addresses for DNS")
	initCmd.Flags().String("v2-registry", "https://registry.nmn/", "URL for default v2 registry used for both helm and containers")
	initCmd.Flags().String("rpm-repository", "https://packages.nmn/repository/shasta-master", "URL for default rpm repository")
	initCmd.Flags().String("can-gateway", "", "Gateway for NCNs on the CAN")
	initCmd.Flags().String("ceph-cephfs-image", "dtr.dev.cray.com/cray/cray-cephfs-provisioner:0.1.0-nautilus-1.3", "The container image for the cephfs provisioner")
	initCmd.Flags().String("ceph-rbd-image", "dtr.dev.cray.com/cray/cray-rbd-provisioner:0.1.0-nautilus-1.3", "The container image for the ceph rbd provisioner")
	initCmd.Flags().String("chart-repo", "http://helmrepo.dev.cray.com:8080", "Upstream chart repo for use during the install")
	initCmd.Flags().String("docker-image-registry", "dtr.dev.cray.com", "Upstream docker registry for use during the install")

	// Site Networking and Preinstall Toolkit Information
	initCmd.Flags().String("install-ncn", "ncn-m001", "Hostname of the node to be used for installation")
	initCmd.Flags().String("install-ncn-bond-members", "p1p1,p1p2", "List of devices to use to form a bond on the install ncn")
	initCmd.Flags().String("site-ip", "", "Site Network Information in the form ipaddress/prefix like 192.168.1.1/24")
	initCmd.Flags().String("site-gw", "", "Site Network IPv4 Gateway")
	initCmd.Flags().String("site-dns", "", "Site Network DNS Server which can be different from the upstream ipv4-resolvers if necessary")
	initCmd.Flags().String("site-nic", "em1", "Network Interface on install-ncn that will be connected to the site network")

	// Default IPv4 Networks
	initCmd.Flags().String("nmn-cidr", shasta.DefaultNMNString, "Overall IPv4 CIDR for all Node Management subnets")
	initCmd.Flags().String("hmn-cidr", shasta.DefaultHMNString, "Overall IPv4 CIDR for all Hardware Management subnets")
	initCmd.Flags().String("can-cidr", shasta.DefaultCANString, "Overall IPv4 CIDR for all Customer Access subnets")
	initCmd.Flags().String("can-static-pool", shasta.DefaultCANStaticString, "Overall IPv4 CIDR for static Customer Access addresses")
	initCmd.Flags().String("can-dynamic-pool", shasta.DefaultCANPoolString, "Overall IPv4 CIDR for dynamic Customer Access addresses")

	initCmd.Flags().String("mtl-cidr", shasta.DefaultMTLString, "Overall IPv4 CIDR for all Provisioning subnets")
	initCmd.Flags().String("hsn-cidr", shasta.DefaultHSNString, "Overall IPv4 CIDR for all HSN subnets")

	initCmd.Flags().Bool("supernet", true, "Use the supernet mask and gateway for NCNs and Switches")

	// Bootstrap VLANS
	initCmd.Flags().Int("nmn-bootstrap-vlan", shasta.DefaultNMNVlan, "Bootstrap VLAN for the NMN")
	initCmd.Flags().Int("hmn-bootstrap-vlan", shasta.DefaultHMNVlan, "Bootstrap VLAN for the HMN")
	initCmd.Flags().Int("can-bootstrap-vlan", shasta.DefaultCANVlan, "Bootstrap VLAN for the CAN")

	// Hardware Details
	initCmd.Flags().Int("mountain-cabinets", 4, "Number of Mountain Cabinets") // 4 mountain cabinets per CDU
	initCmd.Flags().Int("starting-mountain-cabinet", 5000, "Starting ID number for Mountain Cabinets")

	initCmd.Flags().Int("river-cabinets", 1, "Number of River Cabinets")
	initCmd.Flags().Int("starting-river-cabinet", 3000, "Starting ID number for River Cabinets")

	initCmd.Flags().Int("hill-cabinets", 0, "Number of Hill Cabinets")
	initCmd.Flags().Int("starting-hill-cabinet", 9000, "Starting ID number for Hill Cabinets")

	initCmd.Flags().Int("starting-river-NID", 1, "Starting NID for Compute Nodes")
	initCmd.Flags().Int("starting-mountain-NID", 1000, "Starting NID for Compute Nodes")

	// Use these flags to prepare the basecamp metadata json
	initCmd.Flags().String("bgp-asn", "65533", "The autonomous system number for BGP conversations")
	initCmd.Flags().Int("management-net-ips", 0, "Additional number of ip addresses to reserve in each vlan for network equipment")
	initCmd.Flags().Bool("k8s-api-auditing-enabled", false, "Enable the kubernetes auditing API")
	initCmd.Flags().Bool("ncn-mgmt-node-auditing-enabled", false, "Enable management node auditing")

	// Use these flags to set the default ncn bmc credentials for bootstrap
	initCmd.Flags().String("bootstrap-ncn-bmc-user", "", "Username for connecting to the BMC on the initial NCNs")

	initCmd.Flags().String("bootstrap-ncn-bmc-pass", "", "Password for connecting to the BMC on the initial NCNs")

	// Dealing with SLS precursors
	initCmd.Flags().String("hmn-connections", "hmn_connections.json", "HMN Connections JSON Location (For generating an SLS File)")
	initCmd.Flags().String("ncn-metadata", "ncn_metadata.csv", "CSV for mapping the mac addresses of the NCNs to their xnames")
	initCmd.Flags().String("switch-metadata", "switch_metadata.csv", "CSV for mapping the mac addresses of the NCNs to their xnames")
	initCmd.Flags().String("cabinets-yaml", "", "YAML file listing the ids for all cabinets by type")
	initCmd.Flags().String("application-node-config-yaml", "", "YAML to control Application node identification durring the SLS Input File generation")

	// Loftsman Manifest Shasta-CFG
	initCmd.Flags().String("manifest-release", "", "Loftsman Manifest Release Version (leave blank to prevent manifest generation)")
	initCmd.Flags().SortFlags = false
}

func initiailzeManifestDir(url, branch, destination string) {
	// First we need a temporary directory
	dir, err := ioutil.TempDir("", "loftsman-init")
	if err != nil {
		log.Fatalln(err)
	}
	defer os.RemoveAll(dir)
	cloneCmd := exec.Command("git", "clone", url, dir)
	out, err := cloneCmd.Output()
	if err != nil {
		log.Fatalf("cloneCommand finished with error: %s (%v)\n", out, err)
	}
	checkoutCmd := exec.Command("git", "checkout", branch)
	checkoutCmd.Dir = dir
	out, err = checkoutCmd.Output()
	if err != nil {
		if err.Error() != "exit status 1" {
			log.Fatalf("checkoutCommand finished with error: %s (%v)\n", out, err)
		}
	}
	packageCmd := exec.Command("./package/package.sh", "1.4.0")
	packageCmd.Dir = dir
	out, err = packageCmd.Output()
	if err != nil {
		log.Fatalf("packageCommand finished with error: %s (%v)\n", out, err)
	}
	targz, _ := filepath.Abs(filepath.Clean(dir + "/dist/shasta-cfg-1.4.0.tgz"))
	untarCmd := exec.Command("tar", "-zxvvf", targz)
	untarCmd.Dir = destination
	out, err = untarCmd.Output()
	if err != nil {
		log.Fatalf("untarCmd finished with error: %s (%v)\n", out, err)
	}
}

func setupDirectories(systemName string, v *viper.Viper) (string, error) {
	// Set up the path for our base directory using our systemname
	basepath, err := filepath.Abs(filepath.Clean(systemName))
	if err != nil {
		return basepath, err
	}
	// Create our base directory
	if err = os.Mkdir(basepath, 0777); err != nil {
		return basepath, err
	}

	// These Directories make up the overall structure for the Configuration Payload
	// TODO: Refactor this out of the function and into defaults or some other config
	dirs := []string{
		filepath.Join(basepath, "networks"),
		filepath.Join(basepath, "manufacturing"),
		filepath.Join(basepath, "credentials"),
		filepath.Join(basepath, "dnsmasq.d"),
		filepath.Join(basepath, "cpt-files"),
		filepath.Join(basepath, "basecamp"),
	}
	// Add the Manifest directory if needed
	if v.GetString("manifest-release") != "" {
		dirs = append(dirs, filepath.Join(basepath, "loftsman-manifests"))
	}
	// Iterate through the directories and create them
	for _, dir := range dirs {
		if err := os.Mkdir(dir, 0777); err != nil {
			// log.Fatalln("Can't create directory", dir, err)
			return basepath, err
		}
	}
	return basepath, nil
}

func collectInput(v *viper.Viper) ([]shcd_parser.HMNRow, []*shasta.LogicalNCN, []*shasta.ManagementSwitch, shasta.SLSGeneratorApplicationNodeConfig) {
	// The installation requires a set of information in order to proceed
	// First, we need some kind of representation of the physical hardware
	// That is generally represented through the hmn_connections.json file
	// which is literally a cabling map with metadata about the NCNs and
	// River Compute node BMCs, Columbia Rosetta Switches, and PDUs.
	//
	// From the hmn_connections file, we can create a set of HMNRow objects
	// to use for populating SLS.
	hmnRows, err := loadHMNConnectionsFile(v.GetString("hmn-connections"))
	if err != nil {
		log.Fatalf("unable to load hmn connections, %v \n", err)
	}

	// SLS also needs to know about our networking configuration.  In order to do that,
	// we need to load the switches
	switches, err := csiFiles.ReadSwitchCSV(v.GetString("switch-metadata"))
	if err != nil {
		log.Fatalln("Couldn't extract switches", err)
	}

	// Normalize the management switch data, before validation
	for _, mySwitch := range switches {
		mySwitch.Normalize()
	}

	if err := validateSwitchInput(switches); err != nil {
		log.Println("Unable to get reasonable Switches from your csv")
		log.Println("Does your header match the preferred style? Switch Xname,Type,Brand")
		log.Fatal("CSV Parsing failed.  Can't continue.")
	}

	// This is techincally sufficient to generate an SLSState object, but to do so now
	// would not include extended information about the NCNs and Network Switches.
	//
	// The first step in building the NCN map is to read the NCN Metadata file
	ncns, err := csiFiles.ReadNodeCSV(v.GetString("ncn-metadata"))
	if err != nil {
		log.Fatalln("Couldn't extract ncns", err)
	}

	// Normalize the ncn data, before validation
	for _, ncn := range ncns {
		ncn.Normalize()
	}

	if err := validateNCNInput(ncns); err != nil {
		log.Println("Unable to get reasonable NCNs from your csv")
		log.Println("Does your header match the preferred style? Xname,Role,Subrole,BMC MAC,Bootstrap MAC,Bond0 MAC0,Bond0 MAC1")
		log.Fatal("CSV Parsing failed.  Can't continue.")
	}

	// Application Node configration for SLS Config Generator
	// This is an optional input file
	var applicationNodeConfig shasta.SLSGeneratorApplicationNodeConfig
	if v.IsSet("application-node-config-yaml") {
		applicationNodeConfigPath := v.GetString("application-node-config-yaml")

		log.Printf("Using application node config: %s\n", applicationNodeConfigPath)
		err := files.ReadYAMLConfig(applicationNodeConfigPath, &applicationNodeConfig)
		if err != nil {
			log.Fatalf("Unable to parse application-node-config file: %s\nError: %v", applicationNodeConfigPath, err)
		}
	}

	// Normalize application node config
	if err := applicationNodeConfig.Normalize(); err != nil {
		log.Fatalf("Failed to normalize application node config. Error: %s", err)
	}

	// Validate Application node config
	if err := applicationNodeConfig.Validate(); err != nil {
		log.Fatalf("Failed to validate application node config. Error: %s", err)
	}

	return hmnRows, ncns, switches, applicationNodeConfig
}

func validateSwitchInput(switches []*shasta.ManagementSwitch) error {
	// Validate that there is an non-zero number of NCNs extracted from ncn_metadata.csv
	if len(switches) == 0 {
		return fmt.Errorf("unable to extract Switches from switch metadata csv")
	}

	// Validate each Switch
	var mustFail = false
	for _, mySwitch := range switches {
		if err := mySwitch.Validate(); err != nil {
			mustFail = true
			log.Println("Switch from csv is invalid:", err)
		}
	}

	if mustFail {
		return fmt.Errorf("switch_metadata.csv contains invalid switch data")
	}

	return nil
}

func validateNCNInput(ncns []*shasta.LogicalNCN) error {
	// Validate that there is an non-zero number of NCNs extracted from ncn_metadata.csv
	if len(ncns) == 0 {
		return fmt.Errorf("unable to extract NCNs from ncn metadata csv")
	}

	// Validate each NCN
	var mustFail = false
	for _, ncn := range ncns {
		if err := ncn.Validate(); err != nil {
			mustFail = true
			log.Println("NCN from csv is invalid", ncn, err)
		}
	}

	if mustFail {
		return fmt.Errorf("ncn_metadata.csv contains invalid NCN data")
	}

	return nil
}

func mergeNCNs(logicalNcns []*shasta.LogicalNCN, slsNCNs []shasta.LogicalNCN) error {
	// Merge the SLS NCN list with the NCN list from ncn-metadata
	for _, ncn := range logicalNcns {
		found := false
		for _, slsNCN := range slsNCNs {
			if ncn.Xname == slsNCN.Xname {
				// log.Printf("Found match for %v: %v \n", ncn.Xname, tempNCN)
				ncn.Hostname = slsNCN.Hostname
				ncn.Aliases = slsNCN.Aliases
				ncn.BmcPort = slsNCN.BmcPort
				// log.Println("Updated to be :", ncn)

				found = true
				break
			}
		}

		// All NCNs from ncn-metadata need to appear in the generated SLS state
		if !found {
			return fmt.Errorf("failed to find NCN from ncn-metadata in generated SLS State: %s", ncn.Xname)
		}
	}

	return nil
}

func prepareNetworkSLS(shastaNetworks map[string]*shasta.IPV4Network) ([]shasta.IPV4Network, map[string]sls_common.Network) {
	// Fix up the network names & create the SLS friendly version of the shasta networks
	var networks []shasta.IPV4Network
	for name, network := range shastaNetworks {
		if network.Name == "" {
			network.Name = name
		}
		networks = append(networks, *network)
	}
	return networks, convertIPV4NetworksToSLS(&networks)
}

// helper function to maintain a unique list of strings.  Not optimized for large lists.
func appendIfMissing(slice []string, item string) []string {
	for _, ele := range slice {
		if ele == item {
			return slice
		}
	}
	return append(slice, item)
}

func prepareAndGenerateSLS(cd []shasta.CabinetDetail, shastaNetworks map[string]*shasta.IPV4Network, hmnRows []shcd_parser.HMNRow, inputSwitches []*shasta.ManagementSwitch, applicationNodeConfig shasta.SLSGeneratorApplicationNodeConfig, startingNid int) sls_common.SLSState {
	// Management Switch Information is included in the IP Reservations for each subnet
	switchNet, err := shastaNetworks["HMN"].LookUpSubnet("network_hardware")
	if err != nil {
		log.Fatalln("Couldn't find subnet for management switches in the HMN:", err)
	}
	reservedSwitches, _ := extractSwitchesfromReservations(switchNet)
	slsSwitches := make(map[string]sls_common.GenericHardware)
	for _, mySwitch := range reservedSwitches {
		xname := mySwitch.Xname

		// Extract Switch brand from data stored in switch_metdata.csv
		for _, inputSwitch := range inputSwitches {
			if inputSwitch.Xname == xname {
				mySwitch.Brand = inputSwitch.Brand
				break
			}
		}
		if mySwitch.Brand == "" {
			log.Fatalln("Couldn't determine switch brand for:", xname)
		}

		// Create SLS version of the switch
		slsSwitches[mySwitch.Xname], err = convertManagementSwitchToSLS(&mySwitch)
		if err != nil {
			log.Fatalln("Couldn't get SLS management switch representation:", err)
		}
	}

	// Iterate through the cabinets of each kind and build structures that work for SLS Generation
	slsCabinetMap := genCabinetMap(cd, shastaNetworks)

	// Convert shastaNetwork information to SLS Style Networking
	_, slsNetworks := prepareNetworkSLS(shastaNetworks)

	for _, tmpCabinet := range slsCabinetMap["river"] {
		log.Printf("River SLS Cabinet: %s", tmpCabinet.Xname)
	}

	inputState := shasta.SLSGeneratorInputState{
		ApplicationNodeConfig: applicationNodeConfig,
		ManagementSwitches:    slsSwitches,
		RiverCabinets:         slsCabinetMap["river"],
		HillCabinets:          slsCabinetMap["hill"],
		MountainCabinets:      slsCabinetMap["mountain"],
		MountainStartingNid:   startingNid,
		Networks:              slsNetworks,
	}

	slsState := shasta.GenerateSLSState(inputState, hmnRows)
	return slsState
}

func updateReservations(tempSubnet *shasta.IPV4Subnet, logicalNcns []*shasta.LogicalNCN) {
	// Loop the reservations and update the NCN reservations with hostnames
	// we likely didn't have when we registered the resevation
	for index, reservation := range tempSubnet.IPReservations {
		for _, ncn := range logicalNcns {
			if reservation.Comment == ncn.Xname {
				reservation.Name = ncn.Hostname
				reservation.Aliases = append(reservation.Aliases, fmt.Sprintf("%v-%v", ncn.Hostname, strings.ToLower(tempSubnet.NetName)))
				reservation.Aliases = append(reservation.Aliases, fmt.Sprintf("time-%v", strings.ToLower(tempSubnet.NetName)))
				reservation.Aliases = append(reservation.Aliases, fmt.Sprintf("time-%v.local", strings.ToLower(tempSubnet.NetName)))
				if strings.ToLower(ncn.Subrole) == "storage" && strings.ToLower(tempSubnet.NetName) == "hmn" {
					reservation.Aliases = append(reservation.Aliases, "rgw-vip.hmn")
				}
				if strings.ToLower(tempSubnet.NetName) == "nmn" {
					// The xname of a NCN will point to its NMN IP address
					reservation.Aliases = append(reservation.Aliases, ncn.Xname)
				}
				tempSubnet.IPReservations[index] = reservation
			}
			if reservation.Comment == fmt.Sprintf("%v-mgmt", ncn.Xname) {
				reservation.Comment = reservation.Name
				reservation.Aliases = append(reservation.Aliases, fmt.Sprintf("%v-mgmt", ncn.Hostname))
				tempSubnet.IPReservations[index] = reservation
			}
		}
		if tempSubnet.NetName == "NMN" {
			reservation.Aliases = append(reservation.Aliases, fmt.Sprintf("%v.local", reservation.Name))
			tempSubnet.IPReservations[index] = reservation
		}
	}
}

func writeOutput(v *viper.Viper, shastaNetworks map[string]*shasta.IPV4Network, slsState sls_common.SLSState, logicalNCNs []shasta.LogicalNCN, switches []*shasta.ManagementSwitch, globals interface{}) {
	basepath, _ := setupDirectories(v.GetString("system-name"), v)
	err := csiFiles.WriteJSONConfig(filepath.Join(basepath, "sls_input_file.json"), &slsState)
	if err != nil {
		log.Fatalln("Failed to encode SLS state:", err)
	}
	WriteNetworkFiles(basepath, shastaNetworks)
	v.SetConfigType("yaml")
	v.Set("VersionInfo", version.Get())
	v.WriteConfigAs(filepath.Join(basepath, "system_config.yaml"))

	csiFiles.WriteJSONConfig(filepath.Join(basepath, "credentials/root_password.json"), shasta.DefaultRootPW)
	csiFiles.WriteJSONConfig(filepath.Join(basepath, "credentials/bmc_password.json"), shasta.DefaultBMCPW)
	csiFiles.WriteJSONConfig(filepath.Join(basepath, "credentials/mgmt_switch_password.json"), shasta.DefaultNetPW)
	csiFiles.WriteYAMLConfig(filepath.Join(basepath, "customizations.yaml"), shasta.GenCustomizationsYaml(logicalNCNs, shastaNetworks))

	for _, ncn := range logicalNCNs {
		// log.Println("Checking to see if we need CPT files for ", ncn.Hostname)
		if strings.HasPrefix(ncn.Hostname, v.GetString("install-ncn")) {
			log.Println("Generating Installer Node (CPT) interface configurations for:", ncn.Hostname)
			WriteCPTNetworkConfig(filepath.Join(basepath, "cpt-files"), v, ncn, shastaNetworks)
		}
	}
	WriteDNSMasqConfig(basepath, v, logicalNCNs, shastaNetworks)
	WriteConmanConfig(filepath.Join(basepath, "conman.conf"), logicalNCNs)
	WriteMetalLBConfigMap(basepath, v, shastaNetworks, switches)
	WriteBasecampData(filepath.Join(basepath, "basecamp/data.json"), logicalNCNs, shastaNetworks, globals)

	if v.GetString("manifest-release") != "" {
		initiailzeManifestDir(shasta.DefaultManifestURL, "release/shasta-1.4", filepath.Join(basepath, "loftsman-manifests"))
	}
}

func validateFlags() []string {
	var errors []string
	v := viper.GetViper()
	var requiredFlags = []string{
		"system-name",
		"ntp-pool",
		"can-gateway",
		"site-ip",
		"site-gw",
		"site-dns",
		"site-nic",
		"bootstrap-ncn-bmc-user",
		"bootstrap-ncn-bmc-pass",
	}

	for _, flagName := range requiredFlags {
		if !v.IsSet(flagName) {
			errors = append(errors, fmt.Sprintf("%v is required and not set through flag or config file (.%s)", flagName, v.ConfigFileUsed()))
		}
	}

	var ipv4Flags = []string{
		"site-dns",
		"can-gateway",
		"site-gw",
	}
	for _, flagName := range ipv4Flags {
		if v.IsSet(flagName) {
			if net.ParseIP(v.GetString(flagName)) == nil {
				errors = append(errors, fmt.Sprintf("%v should be an ip address and is not set correctly through flag or config file (.%s)", flagName, v.ConfigFileUsed()))
			}
		}
	}

	var cidrFlags = []string{
		"can-cidr",
		"can-static-pool",
		"can-dynamic-pool",
		"nmn-cidr",
		"hmn-cidr",
		"site-ip",
	}

	for _, flagName := range cidrFlags {
		if v.IsSet(flagName) {
			_, _, err := net.ParseCIDR(v.GetString(flagName))
			if err != nil {
				errors = append(errors, fmt.Sprintf("%v should be a CIDR in the form 192.168.0.1/24 and is not set correctly through flag or config file (.%s)", flagName, v.ConfigFileUsed()))
			}
		}
	}
	return errors
}