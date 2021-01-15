package cmd

/*
Copyright 2020 Hewlett Packard Enterprise Development LP
*/
import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// formatCmd represents the format command
var formatCmd = &cobra.Command{
	Use:   "format DISK ISO SIZE",
	Short: "Formats a disk as a LiveCD",
	Long:  `Formats a disk as a LiveCD using an ISO.`,
	// ValidArgs: []string{"disk", "iso", "size"},
	Args: cobra.ExactArgs(3),
	Run: func(cmd *cobra.Command, args []string) {
		writeLiveCD(args[0], args[1], args[2])
	},
}

var isoURL = viper.GetString("iso_url")

var isoName = viper.GetString("iso_name")

var toolkit = viper.GetString("repo_url")

var writeScript = filepath.Join(viper.GetString("write_script"))

func writeLiveCD(device string, iso string, size string) {
	// git clone https://stash.us.cray.com/scm/mtl/cray-pre-install-toolkit.git

	// ./cray-pre-install-toolkit/scripts/write-livecd.sh /dev/sdd $(pwd)/cray-pre-install-toolkit-latest.iso 20000
	// format the device as the liveCD
	cmd := exec.Command(writeScript, device, iso, size)

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &stdoutBuf)
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderrBuf)

	err := cmd.Run()
	if err != nil {
		log.Fatalf("cmd.Run() failed with %s\n", err)
	}
	outStr, errStr := stdoutBuf.String(), stderrBuf.String()
	fmt.Printf("\nout:\n%s\nerr:\n%s\n", outStr, errStr)

	// mount /dev/disk/by-label/PITDATA /mnt/
	fmt.Printf("Run these commands before using 'pit populate':\n")
	fmt.Printf("\tmkdir -pv /mnt/{cow,pitdata}\n")
	fmt.Printf("\tmount -L cow /mnt/cow && mount -L PITDATA /mnt/pitdata\n")
}

func init() {
	pitCmd.AddCommand(formatCmd)
	viper.SetEnvPrefix("pit") // will be uppercased automatically
	viper.AutomaticEnv()
	formatCmd.Flags().StringVarP(&isoURL, "iso-url", "u", viper.GetString("iso_url"), "URL the PIT ISO to download (env: PIT_ISO_URL)")
	formatCmd.Flags().StringVarP(&isoName, "iso-name", "n", viper.GetString("iso_name"), "Local filename of the iso to download (env: PIT_ISO_NAME)")
	formatCmd.MarkFlagRequired("write-script")
	formatCmd.Flags().StringVarP(&writeScript, "write-script", "w", "/usr/local/bin/write-livecd.sh", "Path to the write-livecd.sh script")
	formatCmd.Flags().StringVarP(&toolkit, "repo-url", "r", viper.GetString("repo_url"), "URL of the git repo for the pre-install toolkit (env: PIT_REPO_URL)")
	formatCmd.Flags().BoolP("force", "f", false, "Force overwrite the disk without warning")
}
