/*
Copyright 2021 Hewlett Packard Enterprise Development LP
*/

package cmd

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"github.com/spf13/cobra"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"stash.us.cray.com/HMS/hms-bss/pkg/bssTypes"
	sls_common "stash.us.cray.com/HMS/hms-sls/pkg/sls-common"
)

const gatewayHostname = "api-gw-service-nmn.local"
const s3Prefix = "s3://ncn-images/"

var (
	managementNCNs []sls_common.GenericHardware
	httpClient     *http.Client
)

// handoffCmd represents the handoff command
var handoffCmd = &cobra.Command{
	Use:   "handoff",
	Short: "runs migration steps to transition from LiveCD",
	Long: "A series of subcommands that facilitate the migration of assets/configuration/etc from the LiveCD to the " +
		"production version inside the Kubernetes cluster.",
}

func init() {
	rootCmd.AddCommand(handoffCmd)
}

func setupCommon() {
	var err error

	// These are steps that every handoff function have in common.
	token = os.Getenv("TOKEN")
	if token == "" {
		log.Panicln("Environment variable TOKEN can NOT be blank!")
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{
		InsecureSkipVerify: true,
	}
	httpClient = &http.Client{Transport: transport}

	log.Println("Getting management NCNs from SLS...")
	managementNCNs, err = getManagementNCNsFromSLS()
	if err != nil {
		log.Panicln(err)
	}
	log.Println("Done getting management NCNs from SLS.")
}

func getManagementNCNsFromSLS() (managementNCNs []sls_common.GenericHardware, err error) {
	url := fmt.Sprintf("https://%s/apis/sls/v1/search/hardware?extra_properties.Role=Management",
		gatewayHostname)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		err = fmt.Errorf("failed to create new request: %w", err)
		return
	}
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", token))

	resp, err := httpClient.Do(req)
	if err != nil {
		err = fmt.Errorf("failed to do request: %w", err)
		return
	}

	body, _ := ioutil.ReadAll(resp.Body)
	err = json.Unmarshal(body, &managementNCNs)
	if err != nil {
		err = fmt.Errorf("failed to unmarshal body: %w", err)
	}

	return
}

func uploadEntryToBSS(bssEntry bssTypes.BootParams, method string) {
	url := fmt.Sprintf("https://%s/apis/bss/boot/v1/bootparameters", gatewayHostname)

	jsonBytes, err := json.Marshal(bssEntry)
	if err != nil {
		log.Panicln(err)
	}

	req, err := http.NewRequest(method, url, bytes.NewBuffer(jsonBytes))
	if err != nil {
		log.Panicf("Failed to create new request: %s", err)
	}
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		log.Panicf("Failed to %s BSS entry: %s", method, err)
	}

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := ioutil.ReadAll(resp.Body)
		log.Panicf("Failed to %s BSS entry: %s", method, string(bodyBytes))
	}

	jsonPrettyBytes, _ := json.MarshalIndent(bssEntry, "", "\t")

	log.Printf("Sucessfuly %s BSS entry for %s:\n%s", method, bssEntry.Hosts[0], string(jsonPrettyBytes))
}

func getBSSBootparametersForXname(xname string) bssTypes.BootParams {
	url := fmt.Sprintf("https://%s/apis/bss/boot/v1/bootparameters?name=%s", gatewayHostname, xname)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		log.Panicf("Failed to create new request: %s", err)
	}
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", token))

	resp, err := httpClient.Do(req)
	if err != nil {
		log.Panicf("Failed to get BSS entry: %s", err)
	}

	bodyBytes, _ := ioutil.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		log.Panicf("Failed to put BSS entry: %s", string(bodyBytes))
	}

	// BSS gives back an array.
	var bssEntries []bssTypes.BootParams
	err = json.Unmarshal(bodyBytes, &bssEntries)
	if err != nil {
		log.Panicf("Failed to unmarshal BSS entries: %s", err)
	}

	// We should only ever get one entry for a given xname.
	if len(bssEntries) != 1 {
		log.Panicf("Unexpected number of BSS entries: %+v", bssEntries)
	}

	return bssEntries[0]
}
