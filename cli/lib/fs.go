package lib

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"plandex/types"
	"sync"

	"github.com/plandex/plandex/shared"
)

var Cwd string
var PlandexDir string
var ProjectRoot string
var HomePlandexDir string
var CacheDir string

var CurrentPlanName string
var CurrentPlanDir string
var ResultsSubdir string
var DescriptionsSubdir string
var ConversationSubdir string
var ContextSubdir string

func init() {
	var err error
	Cwd, err = os.Getwd()
	if err != nil {
		panic(err)
	}

	wg := sync.WaitGroup{}

	wg.Add(1)
	go func() {
		defer wg.Done()

		home, err := os.UserHomeDir()
		if err != nil {
			panic(err)
		}
		HomePlandexDir = filepath.Join(home, ".plandex")
		CacheDir = filepath.Join(HomePlandexDir, "cache")
		err = os.MkdirAll(filepath.Join(CacheDir, "tiktoken"), os.ModePerm)
		if err != nil {
			panic(err)
		}
		err = os.Setenv("TIKTOKEN_CACHE_DIR", CacheDir)
		if err != nil {
			panic(err)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()

		PlandexDir, ProjectRoot = findPlandex()

		err = LoadCurrentPlan()
		if err != nil {
			panic(err)
		}
	}()

	wg.Wait()
}

func LoadCurrentPlan() error {
	// Construct the path to the current_plan.json file
	path := filepath.Join(PlandexDir, "current_plan.json")

	// Check if the file exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}

	// Read the contents of the file
	fileBytes, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("error reading current_plan.json: %v", err)
	}

	// Unmarshal the JSON data into the shared.PlanSettings type
	var planSettings types.PlanSettings
	err = json.Unmarshal(fileBytes, &planSettings)
	if err != nil {
		return fmt.Errorf("error unmarshalling current_plan.json: %v", err)
	}

	CurrentPlanName = planSettings.Name
	CurrentPlanDir = filepath.Join(PlandexDir, CurrentPlanName)
	ResultsSubdir = filepath.Join(CurrentPlanDir, "results")
	DescriptionsSubdir = filepath.Join(CurrentPlanDir, "descriptions")
	ConversationSubdir = filepath.Join(CurrentPlanDir, "conversation")
	ContextSubdir = filepath.Join(CurrentPlanDir, "context")

	return nil
}

func FindOrCreatePlandex() (string, bool, error) {
	var err error

	// Determine the directory path
	dir := filepath.Join(Cwd, ".plandex")

	// Check if the directory already exists
	_, err = os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			// If not found, create in the current directory
			err = os.Mkdir(dir, os.ModePerm)
			if err != nil {
				return "", false, err
			}
			PlandexDir = dir
			return dir, true, nil
		}
		return "", false, err
	}

	return dir, false, nil
}

func CwdIsPlan() bool {
	// check if parent directory of cwd is '.plandex'
	parentDir := filepath.Dir(Cwd)
	return parentDir == PlandexDir
}

func ParseInputPaths(fileOrDirPaths []string, params *types.LoadContextParams) ([]string, error) {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	resPaths := []string{}

	for _, path := range fileOrDirPaths {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()

			err := filepath.Walk(p, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}

				mu.Lock()
				defer mu.Unlock()
				if firstErr != nil {
					return firstErr // If an error was encountered, stop walking
				}

				if info.IsDir() {
					if info.Name() == ".git" || info.Name() == ".plandex" {
						return filepath.SkipDir
					}

					if !(params.Recursive || params.NamesOnly) {
						return fmt.Errorf("cannot process directory %s: --recursive or --tree flag not set", path)
					}

					// calculate directory depth from base
					// depth := strings.Count(path[len(p):], string(filepath.Separator))
					// if params.MaxDepth != -1 && depth > params.MaxDepth {
					// 	return filepath.SkipDir
					// }

					if params.NamesOnly {
						// add directory name to results
						resPaths = append(resPaths, path)
					}
				} else {
					// add file path to results
					resPaths = append(resPaths, path)
				}

				return nil
			})

			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}(path)
	}

	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}

	return resPaths, nil
}

func CopyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	// ensure parent directory exists
	err = os.MkdirAll(filepath.Dir(dst), os.ModePerm)
	if err != nil {
		return err
	}

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

func CopyDir(srcDir, dstDir string) error {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(srcDir, entry.Name())
		dstPath := filepath.Join(dstDir, entry.Name())

		if entry.IsDir() {
			err = CopyDir(srcPath, dstPath)
			if err != nil {
				return err
			}
		} else {
			err = CopyFile(srcPath, dstPath)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func GetLatestPlanDescription() (*shared.PlanDescription, error) {
	// List files in descriptions directory
	entries, err := os.ReadDir(DescriptionsSubdir)
	if err != nil {
		return nil, err
	}

	// Find the latest file (named by timestamp)
	var latestFile os.DirEntry
	for _, entry := range entries {
		if latestFile == nil || entry.Name() > latestFile.Name() {
			latestFile = entry
		}
	}

	// Read the contents of the latest file
	bytes, err := os.ReadFile(filepath.Join(DescriptionsSubdir, latestFile.Name()))
	if err != nil {
		return nil, fmt.Errorf("error reading latest description file: %v", err)
	}

	// Unmarshal the JSON data into the shared.PlanDescription type
	var description shared.PlanDescription
	err = json.Unmarshal(bytes, &description)
	if err != nil {
		return nil, fmt.Errorf("error unmarshalling latest description file: %v", err)
	}

	return &description, nil
}

func findPlandex() (string, string) {
	searchPath := Cwd
	for searchPath != "/" {
		dir := filepath.Join(searchPath, ".plandex")
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			return dir, searchPath
		}
		searchPath = filepath.Dir(searchPath)
	}

	return "", ""
}
