//go:generate ../../../tools/readme_config_includer/generator
//go:build linux

package iptables

import (
	_ "embed"
	"errors"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/plugins/inputs"
)

//go:embed sample.conf
var sampleConfig string

var (
	errParse       = errors.New("cannot parse iptables list information")
	chainNameRe    = regexp.MustCompile(`^Chain\s+(\S+)`)
	fieldsHeaderRe = regexp.MustCompile(`^\s*pkts\s+bytes\s+target`)
	valuesRe       = regexp.MustCompile(`^\s*(\d+)\s+(\d+)\s+(\w+).*?/\*\s*(.+?)\s*\*/\s*`)
)

const measurement = "iptables"

type Iptables struct {
	UseSudo bool     `toml:"use_sudo"`
	UseLock bool     `toml:"use_lock"`
	Binary  string   `toml:"binary"`
	Table   string   `toml:"table"`
	Chains  []string `toml:"chains"`

	lister chainLister
}

type chainLister func(table, chain string) (string, error)

func (*Iptables) SampleConfig() string {
	return sampleConfig
}

func (ipt *Iptables) Gather(acc telegraf.Accumulator) error {
	if ipt.Table == "" || len(ipt.Chains) == 0 {
		return nil
	}
	// best effort : we continue through the chains even if an error is encountered,
	// but we keep track of the last error.
	for _, chain := range ipt.Chains {
		data, e := ipt.lister(ipt.Table, chain)
		if e != nil {
			acc.AddError(e)
			continue
		}
		e = ipt.parseAndGather(data, acc)
		if e != nil {
			acc.AddError(e)
			continue
		}
	}
	return nil
}

func (ipt *Iptables) chainList(table, chain string) (string, error) {
	var binary string
	if ipt.Binary != "" {
		binary = ipt.Binary
	} else {
		binary = "iptables"
	}
	iptablePath, err := exec.LookPath(binary)
	if err != nil {
		return "", err
	}
	var args []string
	name := iptablePath
	if ipt.UseSudo {
		name = "sudo"
		args = append(args, iptablePath)
	}
	if ipt.UseLock {
		args = append(args, "-w", "5")
	}
	args = append(args, "-nvL", chain, "-t", table, "-x")
	c := exec.Command(name, args...)
	out, err := c.Output()
	return string(out), err
}

func (ipt *Iptables) parseAndGather(data string, acc telegraf.Accumulator) error {
	lines := strings.Split(data, "\n")
	if len(lines) < 3 {
		return nil
	}
	mchain := chainNameRe.FindStringSubmatch(lines[0])
	if mchain == nil {
		return errParse
	}
	if !fieldsHeaderRe.MatchString(lines[1]) {
		return errParse
	}
	for _, line := range lines[2:] {
		matches := valuesRe.FindStringSubmatch(line)
		if len(matches) != 5 {
			continue
		}

		pkts := matches[1]
		bytes := matches[2]
		target := matches[3]
		comment := matches[4]

		tags := map[string]string{"table": ipt.Table, "chain": mchain[1], "target": target, "ruleid": comment}
		fields := make(map[string]interface{})

		var err error
		fields["pkts"], err = strconv.ParseUint(pkts, 10, 64)
		if err != nil {
			continue
		}
		fields["bytes"], err = strconv.ParseUint(bytes, 10, 64)
		if err != nil {
			continue
		}
		acc.AddFields(measurement, fields, tags)
	}
	return nil
}

func init() {
	inputs.Add("iptables", func() telegraf.Input {
		ipt := &Iptables{}
		ipt.lister = ipt.chainList
		return ipt
	})
}
