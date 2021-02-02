/*
Copyright 2021 Hewlett Packard Enterprise Development LP
*/

package csi

import (
	csiFiles "stash.us.cray.com/MTL/csi/internal/files"
)

// CabinetGroupDetail stores information that can only come from Manufacturing
type CabinetGroupDetail struct {
	Kind            string          `mapstructure:"cabinet-type" yaml:"type" valid:"-"`
	Cabinets        int             `mapstructure:"number" yaml:"total_number" valid:"-"`
	StartingCabinet int             `mapstructure:"starting-cabinet" yaml:"starting_id" valid:"-"`
	CabinetDetails  []CabinetDetail `mapstructure:"cabinets" yaml:"cabinets" valid:"-"`
}

// CabinetDetail stores information about individual cabinets
type CabinetDetail struct {
	ID     int    `mapstructure:"id" yaml:"id" valid:"numeric"`
	Subnet string `mapstructure:"subnet" yaml:"subnet" valid:"-"`
	VlanID int16  `mapstructure:"vlan" yaml:"vlan" valid:"numeric"`
}

// CabinetIDs returns the list of all cabinet ids
func (cgd *CabinetGroupDetail) CabinetIDs() []int {
	var cabinetIds []int
	for _, cab := range cgd.CabinetDetails {
		cabinetIds = append(cabinetIds, cab.ID)
	}
	return cabinetIds
}

// PopulateIds fills out the cabinet ids by doing simple math
func (cgd *CabinetGroupDetail) PopulateIds() {
	if len(cgd.CabinetDetails) < cgd.Cabinets {
		for cabID := cgd.StartingCabinet; cabID < cgd.StartingCabinet+cgd.Cabinets; cabID++ {
			cgd.CabinetDetails = append(cgd.CabinetDetails, CabinetDetail{ID: cabID})
		}
	}
}

// Length returns the expected number of cabinets from the total_number passed in or the length of the cabinet_ids array
func (cgd *CabinetGroupDetail) Length() int {
	if len(cgd.CabinetDetails) == 0 {
		return cgd.Cabinets
	}
	return len(cgd.CabinetDetails)
}

// CabinetTypes returns a list of cabinet types from the file
func (cdf *CabinetDetailFile) CabinetTypes() []string {
	var out []string
	for _, cd := range cdf.Cabinets {
		out = append(out, cd.Kind)
	}
	return out
}

// CabinetDetailFile is a struct that matches the syntax of the configuration file for non-sequential cabinet ids
type CabinetDetailFile struct {
	Cabinets []CabinetGroupDetail `yaml:"cabinets"`
}

// LoadCabinetDetailFile loads the cabinet details from the filesystem
func LoadCabinetDetailFile(path string) (CabinetDetailFile, error) {
	var cabDetailFile CabinetDetailFile
	err := csiFiles.ReadYAMLConfig(path, &cabDetailFile)
	return cabDetailFile, err
}