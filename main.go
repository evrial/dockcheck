package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/jsonmessage"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/moby/term"
)

// Terminal Colors
const (
	ColorReset  = "\033[0m"
	ColorRed    = "\033[0;31m"
	ColorGreen  = "\033[0;32m"
	ColorYellow = "\033[0;33m"
	ColorTeal   = "\033[0;36m"
)

type Config struct {
	AutoUp     bool
	NoUpdates  bool
	IncludeAll bool
	AutoPrune  bool
	Timeout    time.Duration
	MaxAsync   int
	Filter     string
	Excludes   []string
}

type CheckResult struct {
	ContainerSummary container.Summary
	ContainerName    string
	Status           string // "up-to-date", "update-available", "error"
	Error            error
}

func main() {
	cfg := parseFlags()

	// Initialize Native Docker API Client
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		fmt.Printf("%sError connecting to Docker Engine API: %v%s\n", ColorRed, err, ColorReset)
		os.Exit(1)
	}
	defer cli.Close()

	ctx := context.Background()

	// 1. Fetch containers via Docker API
	containers, err := cli.ContainerList(ctx, container.ListOptions{All: cfg.IncludeAll})
	if err != nil {
		fmt.Printf("%sError listing containers: %v%s\n", ColorRed, err, ColorReset)
		os.Exit(1)
	}

	var targetContainers []container.Summary
	for _, c := range containers {
		cName := getCleanName(c)

		if cfg.Filter != "" && !strings.Contains(cName, cfg.Filter) {
			continue
		}
		if isExcluded(cName, cfg.Excludes) {
			continue
		}
		targetContainers = append(targetContainers, c)
	}

	if len(targetContainers) == 0 {
		fmt.Println("No matching containers found.")
		return
	}

	fmt.Printf("Checking %d containers for updates...\n\n", len(targetContainers))

	// 2. Worker Pool for Async Registry Lookups
	results := make(chan CheckResult, len(targetContainers))
	semaphore := make(chan struct{}, cfg.MaxAsync)
	var wg sync.WaitGroup

	for _, c := range targetContainers {
		wg.Add(1)
		go func(ctr container.Summary) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			result := checkContainerUpdate(ctx, cli, ctr, cfg.Timeout)
			results <- result
		}(c)
	}

	wg.Wait()
	close(results)

	// 3. Process Check Results
	var upToDate []string
	var errorsList []string
	var updatesToApply []CheckResult

	for res := range results {
		switch res.Status {
		case "up-to-date":
			upToDate = append(upToDate, res.ContainerName)
		case "update-available":
			updatesToApply = append(updatesToApply, res)
		case "error":
			errorsList = append(errorsList, fmt.Sprintf("%s (%v)", res.ContainerName, res.Error))
		}
	}

	if len(upToDate) > 0 {
		fmt.Printf("%sContainers on latest version:%s\n", ColorGreen, ColorReset)
		for _, name := range upToDate {
			fmt.Printf("  • %s\n", name)
		}
		fmt.Println()
	}

	if len(errorsList) > 0 {
		fmt.Printf("%sContainers with errors:%s\n", ColorRed, ColorReset)
		for _, errStr := range errorsList {
			fmt.Printf("  • %s\n", errStr)
		}
		fmt.Println()
	}

	if len(updatesToApply) > 0 {
		fmt.Printf("%sContainers with updates available:%s\n", ColorYellow, ColorReset)
		for _, item := range updatesToApply {
			fmt.Printf("  • %s\n", item.ContainerName)
		}
		fmt.Println()

		shouldUpdate := !cfg.NoUpdates && (cfg.AutoUp || confirmPrompt("Would you like to update available containers?"))

		if shouldUpdate {
			updatedAny := false
			for _, item := range updatesToApply {
				err := updateContainerNative(ctx, cli, item.ContainerSummary)
				if err != nil {
					fmt.Printf("%sFailed to update %s: %v%s\n", ColorRed, item.ContainerName, err, ColorReset)
				} else {
					fmt.Printf("%sSuccessfully updated %s%s\n", ColorGreen, item.ContainerName, ColorReset)
					updatedAny = true
				}
			}

			// Handle Auto-Prune logic
			if updatedAny {
				shouldPrune := cfg.AutoPrune || (!cfg.AutoUp && confirmPrompt("\nWould you like to prune dangling images?"))
				if shouldPrune {
					pruneImagesNative(ctx, cli)
				}
			}
		}
	} else {
		fmt.Println("No updates available.")
	}
}

// Inspect container and perform remote registry lookup using OCI remote specs
func checkContainerUpdate(ctx context.Context, cli *client.Client, c container.Summary, timeout time.Duration) CheckResult {
	cName := getCleanName(c)

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Inspect container via API
	inspect, _, err := cli.ImageInspectWithRaw(ctx, c.ImageID)
	if err != nil {
		return CheckResult{ContainerName: cName, Status: "error", Error: err}
	}

	if len(inspect.RepoDigests) == 0 {
		return CheckResult{ContainerName: cName, Status: "error", Error: fmt.Errorf("no local repo digest found")}
	}

	ref, err := name.ParseReference(c.Image)
	if err != nil {
		return CheckResult{ContainerName: cName, Status: "error", Error: err}
	}

	desc, err := remote.Get(ref, remote.WithContext(ctx))
	if err != nil {
		return CheckResult{ContainerName: cName, Status: "error", Error: err}
	}

	remoteDigest := desc.Digest.String()

	for _, localDigest := range inspect.RepoDigests {
		if strings.Contains(localDigest, remoteDigest) {
			return CheckResult{ContainerName: cName, Status: "up-to-date"}
		}
	}

	return CheckResult{ContainerSummary: c, ContainerName: cName, Status: "update-available"}
}

// Replaces `docker compose pull` & `docker compose up` using direct Docker API calls
func updateContainerNative(ctx context.Context, cli *client.Client, summary container.Summary) error {
	cName := getCleanName(summary)
	fmt.Printf("\n%sUpdating %s natively via API...%s\n", ColorTeal, cName, ColorReset)

	// 1. Native API Image Pull
	fmt.Printf("Pulling new image: %s\n", summary.Image)
	out, err := cli.ImagePull(ctx, summary.Image, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("image pull failed: %w", err)
	}
	defer out.Close()

	// Format pull progress output to terminal cleanly
	termFd, isTerm := term.GetFdInfo(os.Stdout)
	_ = jsonmessage.DisplayJSONMessagesStream(out, os.Stdout, termFd, isTerm, nil)

	// 2. Inspect original container configuration
	containerInfo, err := cli.ContainerInspect(ctx, summary.ID)
	if err != nil {
		return fmt.Errorf("failed inspecting container: %w", err)
	}

	// 3. Stop running container gracefully
	fmt.Printf("Stopping container %s...\n", cName)
	stopTimeout := 15
	if err := cli.ContainerStop(ctx, summary.ID, container.StopOptions{Timeout: &stopTimeout}); err != nil {
		return fmt.Errorf("failed to stop container: %w", err)
	}

	// 4. Remove old container entity (keeping volumes attached)
	fmt.Printf("Removing old container entity %s...\n", cName)
	if err := cli.ContainerRemove(ctx, summary.ID, container.RemoveOptions{Force: false}); err != nil {
		return fmt.Errorf("failed to remove old container: %w", err)
	}

	// 5. Re-create container with identical original state using new image
	fmt.Printf("Re-creating container %s...\n", cName)
	netConfig := &network.NetworkingConfig{
		EndpointsConfig: containerInfo.NetworkSettings.Networks,
	}

	newContainer, err := cli.ContainerCreate(
		ctx,
		containerInfo.Config,     // Retains Env, Labels, Cmd, Entrypoint, etc.
		containerInfo.HostConfig, // Retains Mounts, Ports, Restart Policy, etc.
		netConfig,                // Pass network configurations
		nil,
		containerInfo.Name,       // Same container name
	)
	if err != nil {
		return fmt.Errorf("failed to create new container: %w", err)
	}

	// 6. Start updated container entity
	fmt.Printf("Starting container %s...\n", cName)
	if err := cli.ContainerStart(ctx, newContainer.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("failed starting container: %w", err)
	}

	return nil
}

// Native API Prune (Replaces `docker image prune -f`)
func pruneImagesNative(ctx context.Context, cli *client.Client) {
	fmt.Printf("\n%sPruning dangling images...%s\n", ColorTeal, ColorReset)

	// Filter for dangling images (`dangling=true`)
	pruneFilters := filters.NewArgs()
	pruneFilters.Add("dangling", "true")

	report, err := cli.ImagesPrune(ctx, pruneFilters)
	if err != nil {
		fmt.Printf("%sFailed to prune images: %v%s\n", ColorRed, err, ColorReset)
		return
	}

	if len(report.ImagesDeleted) > 0 {
		var reclaimedMB float64 = float64(report.SpaceReclaimed) / (1024 * 1024)
		fmt.Printf("%sDeleted %d dangling image(s), reclaimed %.2f MB.%s\n", ColorGreen, len(report.ImagesDeleted), reclaimedMB, ColorReset)
	} else {
		fmt.Println("No dangling images to prune.")
	}
}

// Helpers
func getCleanName(c container.Summary) string {
	if len(c.Names) > 0 {
		return strings.TrimPrefix(c.Names[0], "/")
	}
	return c.ID[:12]
}

func parseFlags() Config {
	var cfg Config
	var excludeRaw string
	var timeoutSec int

	flag.BoolVar(&cfg.AutoUp, "a", false, "Automatic updates without interaction")
	flag.BoolVar(&cfg.NoUpdates, "n", false, "Check availability only")
	flag.BoolVar(&cfg.IncludeAll, "s", false, "Include stopped containers")
	flag.BoolVar(&cfg.AutoPrune, "p", false, "Auto-prune dangling images after update")
	flag.StringVar(&excludeRaw, "e", "", "Comma-separated list of container names to exclude")
	flag.IntVar(&timeoutSec, "t", 10, "Timeout in seconds per container lookup")
	flag.IntVar(&cfg.MaxAsync, "x", 30, "Max concurrent asynchronous lookups")

	flag.Parse()

	if flag.NArg() > 0 {
		cfg.Filter = flag.Arg(0)
	}

	cfg.Timeout = time.Duration(timeoutSec) * time.Second

	if excludeRaw != "" {
		cfg.Excludes = strings.Split(excludeRaw, ",")
	}

	return cfg
}

func isExcluded(name string, excludes []string) bool {
	for _, e := range excludes {
		if strings.TrimSpace(e) == name {
			return true
		}
	}
	return false
}

func confirmPrompt(prompt string) bool {
	var response string
	fmt.Printf("%s [y/N]: ", prompt)
	_, _ = fmt.Scanln(&response)
	response = strings.ToLower(strings.TrimSpace(response))
	return response == "y" || response == "yes"
}