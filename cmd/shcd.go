/*
Copyright 2021 Hewlett Packard Enterprise Development LP
*/

package cmd

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Cray-HPE/cray-site-init/pkg/csi"
	"github.com/Cray-HPE/hms-base/xname"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/xeipuuv/gojsonschema"
	"gopkg.in/yaml.v3"
)

const schema = "shcd-schema.json"
const hmn_connections = "hmn_connections.json"
const switch_metadata = "switch_metadata.csv"
const application_node_config = "application_node_config.yaml"
const ncn_metadata = "ncn_metadata.csv"

var createHMN, createSM, createANC, createNCN bool

var prefixSubroleMapIn map[string]string

var schemaFile, customSchema string

// initCmd represents the init command
var shcdCmd = &cobra.Command{
	Use:   "shcd FILEPATH",
	Short: "Generates hmn_connections.json, switch_metadata.csv, and application_node_config.yaml from an SHCD JSON file",
	Long: `Generates hmn_connections.json, switch_metadata.csv, application_node_config.yaml from an SHCD JSON file.

	It accepts only a valid JSON file, generated by 'canu', which is creates a machine-
	readable format understood by csi.  It is checked against a pre-defined schema and
	if it adhere's to it, it generates the necessary seed files.
	`,
	Args: cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		v := viper.GetViper()
		v.BindPFlags(cmd.Flags())

		if v.IsSet("schema-file") {
			schemaFile = customSchema
		} else {
			schemaFile = filepath.Join("internal/files/", schema)
		}

		// Validate the file passed against the pre-defined schema
		validSHCD, err := ValidateSchema(args[0], schemaFile)

		if err != nil {
			log.Fatalf(err.Error())
		}

		// If the file meets the schema criteria
		if validSHCD {

			// Open the file since we know it is valid
			shcdFile, err := ioutil.ReadFile(args[0])

			if err != nil {
				log.Fatalf(err.Error())
			}

			// Parse the JSON and return an Shcd object
			s, err := ParseSHCD(shcdFile)

			if err != nil {
				log.Fatalf(err.Error())
			}

			if v.IsSet("hmn-connections") {

				createHMNSeed(s, hmn_connections)

			}

			if v.IsSet("switch-metadata") {

				createSwitchSeed(s, switch_metadata)

			}

			if v.IsSet("application-node-config") {

				createANCSeed(s, application_node_config)

			}

			if v.IsSet("ncn-metadata") {

				createNCNSeed(s, ncn_metadata)

			}

		} else {

			log.Printf("- %s\n", err)

			if err != nil {
				log.Fatalf(err.Error())
			}

		}
	},
}

func init() {
	shcdCmd.DisableAutoGenTag = true
	shcdCmd.Flags().SortFlags = true
	shcdCmd.Flags().StringVarP(&customSchema, "schema-file", "j", "", "Use a custom schema file")
	shcdCmd.Flags().BoolVarP(&createHMN, "hmn-connections", "H", false, "Generate the hmn_connections.json file")
	shcdCmd.Flags().BoolVarP(&createNCN, "ncn-metadata", "N", false, "Generate the ncn_metadata.csv file")
	shcdCmd.Flags().BoolVarP(&createSM, "switch-metadata", "S", false, "Generate the switch_metadata.csv file")
	shcdCmd.Flags().BoolVarP(&createANC, "application-node-config", "A", false, "Generate the application_node_config.yaml file")
	shcdCmd.Flags().StringToStringVarP(&prefixSubroleMapIn, "prefix-subrole-mapping", "M", map[string]string{}, "Specify one or more additional <Prefix>=<Subrole> mappings to use when generating application_node_config.yaml. Multiple mappings can be specified in the format of <prefix1>=<subrole1>,<prefix2>=<subrole2>")
}

// The Shcd type represents the entire machine-readable SHCD inside a go struct
type Shcd []Id

// The Id type represents all of the information needed for
type Id struct {
	Architecture string   `json:"architecture"`
	CommonName   string   `json:"common_name"`
	ID           int      `json:"id"`
	Location     Location `json:"location"`
	Model        string   `json:"model"`
	Ports        []Port   `json:"ports"`
	Type         string   `json:"type"`
	Vendor       string   `json:"vendor"`
}

// The Port type defines where things are plugged in
type Port struct {
	DestNodeID int    `json:"destination_node_id"`
	DestPort   int    `json:"destination_port"`
	DestSlot   string `json:"destination_slot"`
	Port       int    `json:"port"`
	Slot       string `json:"slot"`
	Speed      int    `json:"speed"`
}

type Location struct {
	Elevation string `json:"elevation"`
	Rack      string `json:"rack"`
}

// HMNConnections type is the go equivalent structure of hmn_connections.json
type HMNConnections []HMNComponent

// HMNComponent is an individual component in the HMNConnections slice
type HMNComponent struct {
	Source              string `json:"Source"`
	SourceRack          string `json:"SourceRack"`
	SourceLocation      string `json:"SourceLocation"`
	SourceParent        string `json:"SourceParent,omitempty"`
	SourceSubLocation   string `json:"SourceSubLocation,omitempty"`
	DestinationRack     string `json:"DestinationRack"`
	DestinationLocation string `json:"DestinationLocation"`
	DestinationPort     string `json:"DestinationPort"`
}

// NCNMetadata type is the go equivalent structure of ncn_metadata.csv
type NCNMetadata []NcnMacs

// NcnMacs is a row in ncn_metadata.csv
type NcnMacs struct {
	Xname        string
	Role         string
	Subrole      string
	BmcMac       string
	BootstrapMac string
	Bond0Mac0    string
	Bond0Mac1    string
}

// SwitchMetadata type is the go equivalent structure of switch_metadata.csv
type SwitchMetadata []Switch

// Switch is a row in switch_metadata.csv
type Switch struct {
	Xname string
	Type  string
	Brand string
}

// Crafts and prints the xname of a give Id type in the SHCD
func (id Id) GenerateXname() (xn string) {
	// Schema decoder ring:
	// 		cabinet = rack
	// 		chassis = defaults to 0  River: c0, Mountain/Hill: this is the CMM number
	// 		slot = elevation
	// 		space =

	// Each xname has a different structure depending on what the device is
	// This is just a big string of if/else conditionals to determine this
	// At present, this is limited to checking the nodes needed in switch_metadata.csv

	var bmcOrdinal int

	// If it's a CDU switch
	if strings.HasPrefix(id.CommonName, "sw-cdu-") {

		// We need just the number
		i := strings.TrimPrefix(id.CommonName, "sw-cdu-")

		// convert it to an int, which the struct expects
		slot, err := strconv.Atoi(i)

		if err != nil {
			log.Fatalln(err)
		}

		// Create the xname
		// dDwW
		x := xname.CDUMgmtSwitch{
			CoolingGroup: 0,    // D: 0-999
			Slot:         slot, // W: 0-31
		}

		// Convert it to a string
		xn = x.String()

		// Leaf switches have their own needs
	} else if strings.HasPrefix(id.CommonName, "sw-leaf-bmc-") {

		// Get the just number of the elevation
		i := strings.TrimPrefix(id.Location.Elevation, "u")

		// Convert it to an int
		slot, err := strconv.Atoi(i)

		if err != nil {
			log.Fatalln(err)
		}

		// Get the rack as a string
		cabString := id.Location.Rack

		// Strip the "x"
		_, cabNum := utf8.DecodeRuneInString(cabString)

		// Convert to an int
		cabinet, err := strconv.Atoi(cabString[cabNum:])

		if err != nil {
			log.Fatalln(err)
		}

		// Create the xname
		// Chassis defaults to 0 in most cases
		// xXcCwW
		x := xname.MgmtSwitch{
			Cabinet: cabinet, // X: 0-999
			Chassis: 0,       // C: 0-7
			Slot:    slot,    // W: 1-48
		}

		// Convert it to a string
		xn = x.String()

		// Spine switches
	} else if strings.HasPrefix(id.CommonName, "sw-spine") ||
		strings.HasPrefix(id.CommonName, "sw-leaf") {

		// Convert the rack to a string
		cabString := id.Location.Rack

		// Strip the "x"
		_, cabNum := utf8.DecodeRuneInString(cabString)

		// Convert to an int
		cabinet, err := strconv.Atoi(cabString[cabNum:])

		if err != nil {
			log.Fatalln(err)
		}

		// Strip the u
		i := strings.TrimPrefix(id.Location.Elevation, "u")

		// Convert it to an int
		slot, err := strconv.Atoi(i)

		if err != nil {
			log.Fatalln(err)
		}

		// Create the xname
		// Chassis and Space default to 0 and 1 in most cases
		// xXcChHsS
		x := xname.MgmtHLSwitch{
			Cabinet: cabinet, // X: 0-999
			Chassis: 0,       // C: 0-7
			Slot:    slot,    // H: 1-48
			Space:   1,       // S: 1-4
		}

		xn = x.String()

	} else if strings.HasPrefix(id.CommonName, "ncn-") {

		// Convert the rack to a string
		cabString := id.Location.Rack

		// Strip the "x"
		_, cabNum := utf8.DecodeRuneInString(cabString)

		// Convert to an int
		cabinet, err := strconv.Atoi(cabString[cabNum:])

		if err != nil {
			log.Fatalln(err)
		}

		// Strip the u
		i := strings.TrimPrefix(id.Location.Elevation, "u")

		// Check if this is a dense 4 node chassis or dual node chassis as additional logic is needed for these to find the slot number
		if strings.HasSuffix(i, "L") || strings.HasSuffix(i, "R") {
			// Dense 4 node chassis - Gigabyte or Intel chassis --
			// The BMC ordinal for the nodes BMC is derived from the NID of the node, by applying a modulo of 4 plus 1
			if id.Vendor == "gigabyte" || id.Vendor == "intel" {

				i = strings.TrimSuffix(i, "L")
				i = strings.TrimSuffix(i, "R")

				slot, err := strconv.Atoi(i)

				if err != nil {
					log.Fatalln(err)
				}

				bmcOrdinal = (slot % 4) + 1

				// Dual node chassis - Apollo 6500 XL645D -- L == b1, R == b2
			} else if id.Vendor == "hpe" {

				if strings.HasSuffix(i, "L") {

					bmcOrdinal = 1

				} else if strings.HasSuffix(i, "R") {

					bmcOrdinal = 2

				}

			}
		} else {
			// Single node chassis bB is always 0
			bmcOrdinal = 0
		}

		// Convert it to an int
		slot, err := strconv.Atoi(i)

		if err != nil {
			log.Fatalln(err)
		}

		// xCcCsSbBnN
		x := xname.Node{
			Cabinet: cabinet,    // X: 0-999
			Chassis: 0,          // C: 0-7
			Slot:    slot,       // S: 1-63
			BMC:     bmcOrdinal, // B: 0-1 - TODO the HSOS document is wrong here. as we do actually use greater than 1
			// For all river hardware the value of N should be always be 0
			Node: 0, // N: 0-7

		}

		xn = x.String()

	}

	// Return the crafted xname
	return xn
}

// GenerateNCNRoleSubrole generates the appropriate role and subrole based on the ncn-* name
func (id Id) GenerateNCNRoleSubrole() (r string, sr string) {

	if strings.HasPrefix(id.CommonName, "ncn-s") {
		r = "Management"
		sr = "Storage"

	} else if strings.HasPrefix(id.CommonName, "ncn-w") {

		r = "Management"
		sr = "Worker"

	} else if strings.HasPrefix(id.CommonName, "ncn-m") {

		r = "Management"
		sr = "Master"

	}

	// Return the role and subrole ncn_metadata.csv is expecting
	return r, sr
}

// Crafts and prints the switch types that switch_metadata.csv expects
func (id Id) GenerateSwitchType() (st string) {

	// The switch type in switch_metadata.csv differs from the types in the SHCD
	// These conditionals just adjust for the names we expect in that file
	if strings.Contains(id.Architecture, "bmc") {

		st = "Leaf"

	} else if strings.Contains(id.Architecture, "spine") {

		st = "Spine"

	} else if strings.Contains(id.Architecture, "river_ncn_leaf") {

		st = "Aggregation"

	} else if strings.Contains(id.CommonName, "cdu") {

		st = "CDU"
	}

	// Return the switch type switch_metadata.csv is expecting
	return st
}

// Crafts and prints the switch types that switch_metadata.csv expects
func (id Id) GenerateHMNSourceName() (src string) {

	// var prefix string

	// The Source in hmn_connections.json differs from the common_name in the SHCD
	// These conditionals just adjust for the names we expect in that file
	if strings.HasPrefix(id.CommonName, "ncn-m") ||
		strings.HasPrefix(id.CommonName, "ncn-s") ||
		strings.HasPrefix(id.CommonName, "ncn-w") ||
		strings.HasPrefix(id.CommonName, "uan") ||
		strings.HasPrefix(id.CommonName, "cn") ||
		strings.HasPrefix(id.CommonName, "sw-hsn") ||
		strings.HasPrefix(id.CommonName, "x3000p") ||
		strings.HasPrefix(id.CommonName, "lnet") {

		// Get the just number of the elevation
		r := regexp.MustCompile(`\d+`)

		// matches contains the numbers found in the common name
		matches := r.FindAllString(id.CommonName, -1)

		if strings.HasPrefix(id.CommonName, "uan") {

			// if it's a uan, print "uan" and the number
			src = string(id.CommonName[0:3]) + matches[0]

		} else if strings.HasPrefix(id.CommonName, "cn") {

			// if it's a compute node, print "cn" and the number
			src = string(id.CommonName[0:2]) + matches[0]

		} else if strings.HasPrefix(id.CommonName, "lnet") {

			// if it's an lnet, print "lnet" and the number
			src = string(id.CommonName[0:4]) + matches[0]

		} else if strings.HasPrefix(id.CommonName, "x3000p") {

			// if it's a pdu, print the entire name
			src = string(id.CommonName)

		} else if strings.HasPrefix(id.CommonName, "sw-hsn") {

			// if it's a hsn switch, print the entire name
			src = string(id.CommonName)

		} else {

			// if nothing else matches, return an empty string
			src = ""

		}
	}

	// Return the Source name hmn_connections.json is expecting
	return src
}

// createNCNSeed creates ncn_metadata.csv using information from the shcd
func createNCNSeed(shcd Shcd, f string) {
	var ncns NCNMetadata

	// For each entry in the SHCD
	for i := range shcd {

		switch shcd[i].Type {

		// for ncn_metadata.csv, we only care about the servers
		case "server":

			// Only NCNs should be in ncn_metadata.csv
			if !strings.HasPrefix(shcd[i].CommonName, "ncn") {
				continue
			}

			// Generate the xname based on the rules we predefine
			ncnXname := shcd[i].GenerateXname()
			// Do the same for the ncn role and subrole
			ncnRole, ncnSubrole := shcd[i].GenerateNCNRoleSubrole()

			// Create a new Switch type and append it to the SwitchMetadata slice
			ncns = append(ncns, NcnMacs{
				Xname:        ncnXname,
				Role:         ncnRole,
				Subrole:      ncnSubrole,
				BmcMac:       "MAC1",
				BootstrapMac: "MAC2",
				Bond0Mac0:    "MAC3",
				Bond0Mac1:    "MAC4",
			})

		default:
			// Skip anything else since we only need ncns
			continue
		}
	}

	// When writing to csv, the first row should be the headers
	headers := []string{"Xname", "Role", "Subrole", "BMC MAC", "Bootstrap MAC", "Bond0 MAC0", "Bond0 MAC1"}

	// Set up the records we need to write to the file
	// To begin, this contains the headers
	records := [][]string{headers}

	// Then create a new slice with the three pieces of information needed
	for _, v := range ncns {

		row := []string{v.Xname, v.Role, v.Subrole, v.BmcMac, v.BootstrapMac, v.Bond0Mac0, v.Bond0Mac1}

		// Append it to the records slice under the column headers
		records = append(records, row)

	}

	// Create the file object
	ncnmeta, err := os.Create(ncn_metadata)

	if err != nil {
		log.Fatalln(err)
	}

	defer ncnmeta.Close()

	// Create a writer, which will write the data to the file
	writer := csv.NewWriter(ncnmeta)

	defer writer.Flush()

	if err != nil {
		log.Fatalln(err)
	}

	// Create a var for all the records except the header
	r := records[1:]

	// Pass an anonymous function to sort.Slice to sort everything except the headers
	sort.Slice(r, func(i, j int) bool {
		return r[i][0] < r[j][0]
	})

	// For each item in the records slice
	for _, v := range records {
		// Write it to the csv file
		if err := writer.Write(v); err != nil {
			log.Fatalln(err)
		}
	}

	// Let the user know the file was created
	log.Printf("Created %v from SHCD data\n", ncn_metadata)
}

// createSwitchSeed creates switch_metadata.csv using information from the shcd
func createSwitchSeed(shcd Shcd, f string) {
	var switches SwitchMetadata

	// For each entry in the SHCD
	for i := range shcd {

		switch shcd[i].Type {

		// for switch_metadata.csv, we only care about the switches
		case "switch":

			// HSN switch should not be in switch_metadata.csv
			if strings.HasPrefix(shcd[i].CommonName, "sw-hsn") {
				continue
			}

			// Generate the xname based on the rules we predefine
			switchXname := shcd[i].GenerateXname()
			// Do the same for the switch type
			switchType := shcd[i].GenerateSwitchType()
			// The vendor just needs to be capitalized
			switchVendor := strings.Title(shcd[i].Vendor)

			// Create a new Switch type and append it to the SwitchMetadata slice
			switches = append(switches, Switch{
				Xname: switchXname,
				Type:  switchType,
				Brand: switchVendor,
			})

		default:
			// Skip anything else since we only need switches
			continue
		}
	}

	// When writing to csv, the first row should be the headers
	headers := []string{"Switch Xname", "Type", "Brand"}

	// Set up the records we need to write to the file
	// To begin, this contains the headers
	records := [][]string{headers}

	// Then create a new slice with the three pieces of information needed
	for _, v := range switches {

		row := []string{v.Xname, v.Type, v.Brand}

		// Append it to the records slice under the column headers
		records = append(records, row)

	}

	// Create the file object
	sm, err := os.Create(switch_metadata)

	if err != nil {
		log.Fatalln(err)
	}

	defer sm.Close()

	// Create a writer, which will write the data to the file
	writer := csv.NewWriter(sm)

	defer writer.Flush()

	if err != nil {
		log.Fatalln(err)
	}

	// Create a var for all the records except the header
	r := records[1:]

	// Pass an anonymous function to sort.Slice to sort everything except the headers
	sort.Slice(r, func(i, j int) bool {
		return r[i][0] < r[j][0]
	})

	// For each item in the records slice
	for _, v := range records {
		// Write it to the csv file
		if err := writer.Write(v); err != nil {
			log.Fatalln(err)
		}
	}

	// Let the user know the file was created
	log.Printf("Created %v from SHCD data\n", switch_metadata)
}

// createHMNSeed creates hmn_connections.json using information from the shcd
func createHMNSeed(shcd Shcd, f string) {

	var hmn HMNConnections

	// For each entry in the shcd
	for i := range shcd {

		// instantiate a new HMNComponent
		hmnConnection := HMNComponent{}

		// This just aligns the names to better match existing hmn_connections.json's
		// The SHCD and shcd.json all use different names, so why should csi be any different?
		// nodeName := unNormalizeSemiStandardShcdNonName(shcd[i].CommonName)

		// Setting the source name, source rack, source location, is pretty straightforward here
		hmnConnection.Source = shcd[i].CommonName
		hmnConnection.SourceRack = shcd[i].Location.Rack
		hmnConnection.SourceLocation = shcd[i].Location.Elevation

		// Now it starts to get more complex.
		// shcd.json has an array of ports that the device is connected to
		// loop through the ports and find the destination id, which can be used
		// to find the destination info
		for p := range shcd[i].Ports {
			// get the id of the destination node, so it can be easily used an an index
			destId := shcd[i].Ports[p].DestNodeID
			// Special to this hmn_connections.json file, we need this SubRack/dense node stuff
			// if the node is a dense compute node--indiciated by L or R in the location,
			// we need to add the SourceSubLocation and SourceParent
			// There should be a row in the shcd that has the SubRack name, which
			// shares the same u location as the entries with the L or R in the location
			if strings.HasSuffix(shcd[i].Location.Elevation, "L") || strings.HasSuffix(shcd[i].Location.Elevation, "R") {
				// hmnConnection.SourceSubLocation = shcd[i].Location.Rack
				hmnConnection.SourceParent = "FIXME INSERT SUBRACK HERE"
				// FIXME: remove above and uncomment below when we have a way to get the subrack name
				// hmnConnection.SourceParent = fmt.Sprint(shcd[destId].CommonName)
			}

			// Now use the destId again to set the destination info
			hmnConnection.DestinationRack = shcd[destId].Location.Rack
			hmnConnection.DestinationLocation = shcd[destId].Location.Elevation
			hmnConnection.DestinationPort = fmt.Sprint("j", shcd[i].Ports[p].DestPort)
		}

		// finally, append the created HMNComponent to the HMNConnections slice
		// This slice will be what is written to the file as hmn_connections.json
		hmn = append(hmn, hmnConnection)
	}

	// Indent the file for better human-readability
	file, _ := json.MarshalIndent(hmn, "", " ")

	// Write the file to disk
	_ = ioutil.WriteFile(hmn_connections, file, 0644)

	log.Printf("Created %v from SHCD data\n", hmn_connections)

}

// createANCSeed creates application_node_config.yaml using information from the shcd
func createANCSeed(shcd Shcd, f string) error {

	var (
		comment1 string = "# Additional application node prefixes to match in the hmn_connections.json file"
		comment2 string = "\n# Additional HSM SubRoles"
		comment3 string = "\n# Application Node aliases"
	)

	anc := csi.SLSGeneratorApplicationNodeConfig{
		Prefixes:          make([]string, 0, 1),
		PrefixHSMSubroles: make(map[string]string),
		Aliases:           make(map[string][]string),
	}
	prefixMap := make(map[string]string)

	// Search the shcd for Application Nodes
	for _, id := range shcd {
		source := strings.ToLower(id.CommonName)
		idType := strings.ToLower(id.Type)
		if idType != "server" ||
			strings.Contains(source, "ncn") {
			continue
		}

		found := false
		// Match custom prefix<->subrole mappings first before default ones
		if len(prefixSubroleMapIn) > 0 {
			for prefix, subrole := range prefixSubroleMapIn {
				if strings.HasPrefix(source, prefix) {
					found = true
					prefixMap[prefix] = subrole
					break
				}
			}
		}
		if !found {
			// Match default prefix<->subrole mappings
			for _, prefix := range csi.DefaultApplicationNodePrefixes {
				if strings.HasPrefix(source, prefix) {
					found = true
					break
				}
			}
		}

		if !found {
			// Add a placeholder for unmatched prefixes to have the admin
			// assign a subrole to use for that prefix.
			f := strings.FieldsFunc(source,
				func(c rune) bool { return !unicode.IsLetter(c) })
			prefixMap[f[0]] = "~fixme~"
		}

		location := strings.TrimFunc(id.Location.Elevation,
			func(r rune) bool { return unicode.IsLetter(r) })

		// Construct the xname
		xname := ""
		if strings.HasSuffix(strings.ToLower(id.Location.Elevation), "l") {
			xname = fmt.Sprintf("%sc0s%sb1n0", id.Location.Rack, location)
		} else if strings.HasSuffix(strings.ToLower(id.Location.Elevation), "r") {
			xname = fmt.Sprintf("%sc0s%sb2n0", id.Location.Rack, location)
		} else {
			xname = fmt.Sprintf("%sc0s%sb0n0", id.Location.Rack, location)
		}

		// List Aliases
		if _, ok := anc.Aliases[xname]; !ok {
			anc.Aliases[xname] = make([]string, 0, 1)
		}
		anc.Aliases[xname] = append(anc.Aliases[xname], source)
	}

	// Build the 'Prefixes' list and the 'PrefixHSMSubroles' map
	for prefix, subrole := range prefixMap {
		anc.Prefixes = append(anc.Prefixes, prefix)
		anc.PrefixHSMSubroles[prefix] = subrole
		// Warn the admin if there are any prefixes that have no subrole
		if subrole == csi.SubrolePlaceHolder {
			log.Printf("WARNING: Prefix '%s' has no subrole mapping. Replace `%s` placeholder with a valid subrole in the resulting %s.\n", prefix, csi.SubrolePlaceHolder, application_node_config)
		}
	}

	// Format the yaml
	prefixNodes := []*yaml.Node{}
	prefixHSMSubroleNodes := []*yaml.Node{}
	sort.Strings(anc.Prefixes)
	for _, prefix := range anc.Prefixes {
		n := yaml.Node{Kind: yaml.ScalarNode, Value: prefix}
		prefixNodes = append(prefixNodes, &n)

		subrole := anc.PrefixHSMSubroles[prefix]
		kn := yaml.Node{Kind: yaml.ScalarNode, Value: prefix}
		vn := yaml.Node{Kind: yaml.ScalarNode, Value: subrole}
		prefixHSMSubroleNodes = append(prefixHSMSubroleNodes, &kn, &vn)
	}
	prefixes := yaml.Node{Kind: yaml.SequenceNode, Content: prefixNodes}
	prefixesTitle := yaml.Node{Kind: yaml.ScalarNode, Value: "prefixes", HeadComment: comment1}
	prefixHSMSubroles := yaml.Node{Kind: yaml.MappingNode, Content: prefixHSMSubroleNodes}
	prefixHSMSubrolesTitle := yaml.Node{Kind: yaml.ScalarNode, Value: "prefix_hsm_subroles", HeadComment: comment2}

	aliasNodes := []*yaml.Node{}
	aliasArray := make([]string, 0, 1)
	for xname, _ := range anc.Aliases {
		aliasArray = append(aliasArray, xname)
	}
	sort.Strings(aliasArray)
	for _, xname := range aliasArray {
		aliasList := anc.Aliases[xname]
		kn := yaml.Node{Kind: yaml.ScalarNode, Value: xname}
		aliasSubNodes := []*yaml.Node{}
		for _, alias := range aliasList {
			n := yaml.Node{Kind: yaml.ScalarNode, Style: yaml.DoubleQuotedStyle, Value: alias}
			aliasSubNodes = append(aliasSubNodes, &n)
		}
		vn := yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle, Content: aliasSubNodes}
		aliasNodes = append(aliasNodes, &kn, &vn)
	}
	aliases := yaml.Node{Kind: yaml.MappingNode, Content: aliasNodes}
	aliasesTitle := yaml.Node{Kind: yaml.ScalarNode, Value: "aliases", HeadComment: comment3}

	ancYaml := yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{&prefixesTitle, &prefixes, &prefixHSMSubrolesTitle, &prefixHSMSubroles, &aliasesTitle, &aliases}}

	ancFile, err := os.Create(application_node_config)

	if err != nil {
		log.Fatalln(err)
	}
	defer ancFile.Close()
	_, err = ancFile.WriteString("---\n")
	e := yaml.NewEncoder(ancFile)
	defer e.Close()
	e.SetIndent(2)
	err = e.Encode(ancYaml)
	log.Printf("Created %v from SHCD data\n", application_node_config)
	return err
}

// ValidateSchema compares a JSON file to the defined schema file
func ValidateSchema(f string, s string) (bool, error) {
	// First validate the file passed in conforms to the schema
	schema := "file://" + s
	schemaLoader := gojsonschema.NewReferenceLoader(schema)
	jsonFile := "file://" + f
	documentLoader := gojsonschema.NewReferenceLoader(jsonFile)

	result, err := gojsonschema.Validate(schemaLoader, documentLoader)

	if err != nil {
		return false, fmt.Errorf("%s", err)
	}

	// If the json passed in does not meet the schema requirements, error
	if !result.Valid() {

		for _, desc := range result.Errors() {
			return false, fmt.Errorf("SHCD schema error: %s", desc)
		}

	}

	return true, nil
}

// ParseSHCD accepts a machine-readable SHCD and produces an Shcd object, which can be used throughout csi
// It is the golang and csi equivalent of the shcd.json file generated by canu
func ParseSHCD(f []byte) (Shcd, error) {
	var shcd Shcd

	// unmarshall it
	err := json.Unmarshal(f, &shcd)

	if err != nil {
		fmt.Println("error:", err)
		return shcd, err
	}

	return shcd, nil
}
