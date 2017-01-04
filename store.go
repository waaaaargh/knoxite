/*
 * knoxite
 *     Copyright (c) 2016-2017, Christian Muehlhaeuser <muesli@gmail.com>
 *
 *   For license see LICENSE
 */

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/shutdown2"
	"github.com/muesli/goprogressbar"
	"github.com/spf13/cobra"

	knoxite "github.com/knoxite/knoxite/lib"
)

// Error declarations
var (
	ErrRedundancyAmount = errors.New("failure tolerance can't be equal or higher as the number of storage backends")
)

// StoreOptions holds all the options that can be set for the 'store' command
type StoreOptions struct {
	Description      string
	Compression      string
	Encryption       string
	FailureTolerance uint
}

var (
	storeOpts = StoreOptions{}

	storeCmd = &cobra.Command{
		Use:   "store <volume> <dir/file> [...]",
		Short: "store files/directories",
		Long:  `The store command creates a snapshot of a file or directory`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 1 {
				return fmt.Errorf("store needs to know which volume to create a snapshot in")
			}
			if len(args) < 2 {
				return fmt.Errorf("store needs to know which files and/or directories to work on")
			}
			return executeStore(args[0], args[1:], storeOpts)
		},
	}
)

func init() {
	storeCmd.Flags().StringVarP(&storeOpts.Description, "desc", "d", "", "a description or comment for this volume")
	storeCmd.Flags().StringVarP(&storeOpts.Compression, "compression", "c", "", "compression algo to use: none (default), gzip")
	storeCmd.Flags().StringVarP(&storeOpts.Encryption, "encryption", "e", "", "encryption algo to use: aes (default), none")
	storeCmd.Flags().UintVarP(&storeOpts.FailureTolerance, "tolerance", "t", 0, "failure tolerance against n backend failures")

	RootCmd.AddCommand(storeCmd)
}

func store(repository *knoxite.Repository, chunkIndex *knoxite.ChunkIndex, snapshot *knoxite.Snapshot, targets []string, opts StoreOptions) error {
	// we want to be notified during the first phase of a shutdown
	cancel := shutdown.First()

	fmt.Println()
	overallProgressBar := goprogressbar.NewProgressBar("Overall Progress", 0, 0, 60)
	wd, gerr := os.Getwd()
	if gerr != nil {
		return gerr
	}

	if uint(len(repository.Backend.Backends))-opts.FailureTolerance <= 0 {
		return ErrRedundancyAmount
	}

	progress := snapshot.Add(wd, targets, *repository, chunkIndex,
		strings.ToLower(opts.Compression) == strings.ToLower(CompressionText(knoxite.CompressionGZip)),
		strings.ToLower(opts.Encryption) != strings.ToLower(EncryptionText(knoxite.EncryptionNone)),
		uint(len(repository.Backend.Backends))-opts.FailureTolerance, opts.FailureTolerance)

	fileProgressBar := goprogressbar.NewProgressBar("", 0, 0, 60)
	lastPath := ""

	for p := range progress {
		select {
		case n := <-cancel:
			fmt.Println("Aborting...")
			close(n)
			return nil

		default:
			if p.Error != nil {
				fmt.Println()
				return p.Error
			}
			if p.Path != lastPath && lastPath != "" {
				fmt.Println()
			}
			fileProgressBar.Total = int64(p.CurrentItemStats.Size)
			fileProgressBar.Current = int64(p.CurrentItemStats.Transferred)
			fileProgressBar.RightAlignedText = fmt.Sprintf("%s / %s  %s/s",
				knoxite.SizeToString(uint64(fileProgressBar.Current)),
				knoxite.SizeToString(uint64(fileProgressBar.Total)),
				knoxite.SizeToString(p.TransferSpeed()))

			overallProgressBar.Total = int64(p.TotalStatistics.Size)
			overallProgressBar.Current = int64(p.TotalStatistics.Transferred)
			overallProgressBar.RightAlignedText = fmt.Sprintf("%s / %s",
				knoxite.SizeToString(uint64(overallProgressBar.Current)),
				knoxite.SizeToString(uint64(overallProgressBar.Total)))

			if p.Path != lastPath {
				lastPath = p.Path
				fileProgressBar.Text = p.Path
			}

			goprogressbar.MoveCursorUp(1)
			fileProgressBar.LazyPrint()
			goprogressbar.MoveCursorDown(1)
			overallProgressBar.LazyPrint()
		}
	}

	fmt.Printf("\nSnapshot %s created: %s\n", snapshot.ID, snapshot.Stats.String())
	return nil
}

func executeStore(volumeID string, args []string, opts StoreOptions) error {
	targets := []string{}
	for _, target := range args {
		if absTarget, err := filepath.Abs(target); err == nil {
			target = absTarget
		}
		targets = append(targets, target)
	}

	// filter here? exclude/include?

	// acquire a shutdown lock. we don't want these next calls to be interrupted
	lock := shutdown.Lock()
	if lock == nil {
		return nil
	}
	repository, err := openRepository(globalOpts.Repo, globalOpts.Password)
	if err != nil {
		return err
	}
	volume, err := repository.FindVolume(volumeID)
	if err != nil {
		return err
	}
	snapshot, err := knoxite.NewSnapshot(opts.Description)
	if err != nil {
		return err
	}
	chunkIndex, err := knoxite.OpenChunkIndex(&repository)
	if err != nil {
		return err
	}
	// release the shutdown lock
	lock()

	err = store(&repository, &chunkIndex, snapshot, targets, opts)
	if err != nil {
		return err
	}

	// acquire another shutdown lock. we don't want these next calls to be interrupted
	lock = shutdown.Lock()
	if lock == nil {
		return nil
	}
	defer lock()

	err = snapshot.Save(&repository)
	if err != nil {
		return err
	}
	err = volume.AddSnapshot(snapshot.ID)
	if err != nil {
		return err
	}
	err = chunkIndex.Save(&repository)
	if err != nil {
		return err
	}
	return repository.Save()
}

// CompressionText returns a user-friendly string indicating the compression algo that was used
func CompressionText(enum int) string {
	switch enum {
	case knoxite.CompressionNone:
		return "none"
	case knoxite.CompressionGZip:
		return "GZip"
	case knoxite.CompressionLZW:
		return "LZW"
	case knoxite.CompressionFlate:
		return "Flate"
	case knoxite.CompressionZlib:
		return "zlib"
	}

	return "unknown"
}

// EncryptionText returns a user-friendly string indicating the encryption algo that was used
func EncryptionText(enum int) string {
	switch enum {
	case knoxite.EncryptionNone:
		return "none"
	case knoxite.EncryptionAES:
		return "AES"
	}

	return "unknown"
}
