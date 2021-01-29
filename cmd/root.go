/*
Copyright 2021 Hewlett Packard Enterprise Development LP
*/

package cmd

import (
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"

	"github.com/mitchellh/go-homedir"
	"github.com/spf13/cobra"

	"github.com/spf13/viper"
)

var cfgFile string

const (
	defaultConfigFilename = "csi"
	envPrefix             = "csi"
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "csi",
	Short: "Cray Site Init. for new sites ore re-installs and upgrades.",
	Long: `
CSI creates, validates, installs, and upgrades a CRAY supercomputer or HPCaaS platform.

It supports initializing a set of configuration from a variety of inputs including 
flags (and/or Shasta 1.3 configuration files). It can also validate that a set of 
configuration details are accurate before attempting to use them for installation.

Configs aside, this will prepare USB sticks for deploying on baremetal or for recovery and
triage.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return initializeFlagswithViper(cmd)
	},
	Run: func(cmd *cobra.Command, args []string) {
		cmd.Usage()
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

// This function is useful for understanding what a particular viper contains.
// It is more a crutch for development than anything I would ever expect a customer to see.
func viperWiper(v *viper.Viper) {
	fmt.Print("\n === Viper Wiper === \n\n")
	for _, name := range v.AllKeys() {
		fmt.Println("Key: ", name, " => Name:", v.GetString(name))
	}
	fmt.Print("\n === Viper Wiper Done === \n\n")
}

// This function maps all pflags to strings in viper
func initializeFlagswithViper(cmd *cobra.Command) error {
	v := viper.GetViper()

	if cfgFile != "" {
		// Use config file from the flag.
		v.SetConfigFile(cfgFile)
	} else {
		// Find home directory.
		home, err := homedir.Dir()
		if err != nil {
			log.Fatalf("Home Directory not found: %v", err)
		}
		// Add the home directory to the config path
		v.AddConfigPath(home)

		// Add the local directory to the config path
		v.AddConfigPath(".")
		// Set the base name of the config file, without the file extension.
		v.SetConfigName(defaultConfigFilename)
	}

	// Attempt to read the config file, gracefully ignoring errors
	// caused by a config file not being found. Return an error
	// if we cannot parse the config file.
	if err := v.ReadInConfig(); err != nil {
		// It's okay if there isn't a config file
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return err
		}
	}

	// When we bind flags to environment variables expect that the
	// environment variables are prefixed, e.g. a flag like --number
	// binds to an environment variable STRING_NUMBER. This helps
	// avoid conflicts.
	reg, err := regexp.Compile("[^A-Za-z0-9]+")
	if err != nil {
		log.Fatal(err)
	}
	// "csi config init --option" => csi_CONFIG_INIT_OPTION
	v.SetEnvPrefix(reg.ReplaceAllString(cmd.CommandPath(), "_"))

	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))

	// Bind to environment variables
	// Works great for simple config names, but needs help for names
	// like --favorite-color which we fix in the bindFlags function
	v.AutomaticEnv()

	// Bind the current command's flags to viper
	v.BindPFlags(cmd.Flags())

	return nil
}
