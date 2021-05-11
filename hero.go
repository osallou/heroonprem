package heroonprem

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
	"time"

	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v2"
)

// HeroGlobalConfig base config for library
type HeroGlobalConfig struct {
	ScriptDir string
}

type METHOD string

const FILE_ADD METHOD = "add"
const FILE_EDIT METHOD = "edit"
const FILE_DELETE METHOD = "delete"

func NewHeroGlobalConfig(dir string) *HeroGlobalConfig {
	if dir == "" {
		dir = "/tmp"
	}
	return &HeroGlobalConfig{
		ScriptDir: dir,
	}
}

type HeroJob struct {
	Methods []METHOD `yaml:"methods"` // List of methods rules should be applied, if empty match only for add
	Rules   []string `yaml:"rules"`
	Scripts []string `yaml:"scripts"`
	Cpus    int64    `yaml:"cpus"`  // number of cpus
	Mem     int64    `yaml:"mem"`   // In Gb
	Time    string   `yaml:"time"`  // 00:05:00  hrs:min:sec
	Queue   string   `yaml:"queue"` // Partition name
}

type HeroConfig struct {
	Hero map[string]HeroJob `yaml:"hero"`
}

type HeroUser struct {
	Name string
	UID  uint32
	GID  uint32
	Home string
}

type HeroExperiment struct {
	User       *HeroUser
	Experiment string
	Created    string
	File       string
	BaseDir    string
	Scripts    []string
	Cpus       int64
	Mem        int64
	Queue      string
	Time       string
	Method     METHOD
}

const scriptTemplate = `#!/bin/bash
#SBATCH --job-name={{.Experiment}}
#SBATCH -e {{.BaseDir}}/{{.Experiment}}-%j.err
#SBATCH -o {{.BaseDir}}/{{.Experiment}}-%j.out
#SBATCH --uid {{.User.UID}}
#SBATCH --uid {{.User.GID}}

#SBATCH --chdir {{.BaseDir}}
{{if .Queue}}
#SBATCH --partition={{.Queue}}
{{end}}
{{if .Cpus}}
#SBATCH --cpus-per-task={{.Cpus}}
{{end}}
{{if .Mem }}
#SBATCH --mem={{.Mem}}G
{{end}}
{{if .Time}}
#SBATCH --time={{.Time}}
{{end}}
set -e
# User: {{.User.Name}}
# File: {{.File}}
# Experiment: {{.Experiment}}
# Date: {{.Created}}
export FILE={{.File}}
export WORKDIR={{.BaseDir}}
export METHOD={{.Method}}
echo "########################"
date
echo "########################"
if [ ! -e $FILE ]; then
    echo "File $FILE not found"
    exit 1
fi
{{range .Scripts}}
echo "########################"
date
echo "########################"
{{.}}
{{end}}
`

func getJobConfig(file string, user *HeroUser) (*HeroConfig, error) {
	heroHomeCfgFile := ""

	heroCfg := filepath.Join(user.Home, ".hero")
	if _, err := os.Stat(heroCfg); err == nil {
		heroHomeCfgFile = heroCfg
	}

	heroCfgFile := ""
	baseDir := filepath.Dir(file)
	filePaths := strings.Split(baseDir, "/")
	for len(filePaths) > 0 {
		heroCfg := filepath.Join(strings.Join(filePaths, "/"), ".hero")
		log.Debug().Msgf("[check] %s\n", heroCfg)
		if _, err := os.Stat(heroCfg); err == nil {
			heroCfgFile = heroCfg
			break
		}
		fileLen := len(filePaths) - 1
		filePaths = filePaths[:fileLen]
	}
	if heroCfgFile == "" && heroHomeCfgFile == "" {
		return nil, fmt.Errorf("no .hero found")
	}

	if heroCfgFile == "" {
		log.Debug().Msg("No .hero in file hierarchy, use home defaults")
		heroCfgFile = heroHomeCfgFile
	}
	log.Info().Msgf("[.hero=%s]\n", heroCfgFile)
	data, err := ioutil.ReadFile(heroCfgFile)
	if err != nil {
		log.Error().Err(err).Msg("could not read config file")
		return nil, err
	}
	var heroConfig HeroConfig
	err = yaml.Unmarshal(data, &heroConfig)
	if err != nil {
		return nil, err
	}

	return &heroConfig, nil
}

func getScript(method METHOD, file string, experiment string, job HeroJob, user *HeroUser) (string, error) {
	scripts, err := getScripts(file, experiment, job)
	if err != nil {
		return "", err
	}

	exp := HeroExperiment{
		User:       user,
		Experiment: experiment,
		Created:    time.Now().String(),
		File:       file,
		Scripts:    scripts,
		Cpus:       job.Cpus,
		Mem:        job.Mem,
		Time:       job.Time,
		Queue:      job.Queue,
		BaseDir:    filepath.Dir(file),
		Method:     method,
	}
	var data bytes.Buffer
	tpl, err := template.New("experiment").Parse(scriptTemplate)
	if err != nil {
		log.Error().Err(err).Msg("template error")
		return "", err
	}
	err = tpl.Execute(&data, exp)
	if err != nil {
		log.Error().Err(err).Msg("template error")
		return "", err
	}
	return data.String(), nil
}

func getScripts(file string, experiment string, job HeroJob) ([]string, error) {
	scripts := make([]string, 0)
	type ScriptData struct {
		File string
	}
	for _, script := range job.Scripts {
		var data bytes.Buffer
		tpl, err := template.New("file").Parse(script)
		if err != nil {
			log.Error().Err(err).Msg("template error")
			return nil, err
		}
		input := ScriptData{
			File: file,
		}
		err = tpl.Execute(&data, input)
		if err != nil {
			log.Error().Err(err).Msg("template error")
			return nil, err
		}
		scripts = append(scripts, data.String())

	}
	return scripts, nil
}

// CreateJob creates a bash script for slurm use
func CreateJob(file string, method METHOD, user *HeroUser, cfg *HeroGlobalConfig) (string, error) {
	// Get user home dir from ldap
	// Check if exists ~/.hero
	// Check also recursively in subdirs of file
	// If yes read yaml
	/*
		hero:
		  experiment1:
		    rules:
		    - path_regexp1
			- path_regexp2
			script:
			- "run_script.sh -a -v ...  {{.File}}"
	*/
	// If rule patch file, run scripts
	fullPath, err := filepath.Abs(file)
	if err != nil {
		return "", err
	}
	jcfg, err := getJobConfig(fullPath, user)
	if err != nil {
		log.Debug().Msgf("[file=%s] no .hero found, skipping", file)
		return "", nil
	}
	var job HeroJob
	experiment := ""
	for name, exp := range jcfg.Hero {
		methodOK := false
		if exp.Methods == nil || len(exp.Methods) == 0 {
			if method != FILE_ADD {
				log.Debug().Msgf("[expirement=%s][method=%s] skipping", name, method)
				continue
			}
			methodOK = true
		}
		for _, m := range exp.Methods {
			if m == method {
				methodOK = true
				break
			}
		}
		if !methodOK {
			log.Debug().Msgf("[expirement=%s][method=%s] skipping", name, method)
		}
		for _, r := range exp.Rules {
			log.Debug().Msgf("[experiment=%s][rule=%s]", name, r)
			re := regexp.MustCompile(r)
			if re.Match([]byte(fullPath)) {
				job = exp
				experiment = name
				break
			}
		}
	}
	if experiment == "" {
		log.Debug().Msg("[expirement=no]")
		return "", nil
	}
	log.Debug().Msgf("[experiment=%s] %+v", experiment, job)

	script, err := getScript(method, fullPath, experiment, job, user)
	if err != nil {
		return "", err
	}
	log.Debug().Msgf("[script] %s", script)
	ts := time.Now().Unix()
	scriptName := filepath.Join(cfg.ScriptDir, fmt.Sprintf("%s_%d.sh", experiment, ts))
	ioutil.WriteFile(scriptName, []byte(script), 0755)
	return scriptName, nil
}

func CallJob(script string, user HeroUser, cfg *HeroGlobalConfig) error {
	scriptRealPath, err := filepath.Abs(script)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(scriptRealPath, cfg.ScriptDir) {
		return fmt.Errorf("script not in %s", cfg.ScriptDir)
	}
	cmd := exec.Command("sbatch", scriptRealPath)
	/*
		cmd.SysProcAttr = &syscall.SysProcAttr{}
		cmd.SysProcAttr.Credential = &syscall.Credential{Uid: user.UID, Gid: user.GID}
	*/
	var outb, errb bytes.Buffer
	cmd.Stdout = &outb
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		log.Error().Err(err).Msgf("[calljob][job=%s] execution error", script)
		return err
	}
	log.Debug().Msgf("[script=%s][out] %s", script, outb.String())
	log.Debug().Msgf("[script=%s][err] %s", script, errb.String())
	return nil
}
