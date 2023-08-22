// Modifications Copyright 2018 The klaytn Authors
// Copyright 2019 The go-ethereum Authors
// This file is part of go-ethereum.
//
// go-ethereum is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// go-ethereum is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with go-ethereum. If not, see <http://www.gnu.org/licenses/>.
//
// This file is derived from cmd/abigen/main.go (2018/06/04).
// Modified and improved for the klaytn development.

package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"regexp"
	"strings"

	"github.com/klaytn/klaytn/accounts/abi/bind"
	"github.com/klaytn/klaytn/cmd/utils"
	"github.com/klaytn/klaytn/common/compiler"
	"github.com/klaytn/klaytn/crypto"
	"github.com/klaytn/klaytn/log"
	"github.com/urfave/cli/v2"
)

const (
	commandHelperTemplate = `{{.Name}}{{if .Subcommands}} command{{end}}{{if .Flags}} [command options]{{end}} [arguments...]
{{if .Description}}{{.Description}}
{{end}}{{if .Subcommands}}
SUBCOMMANDS:
	{{range .Subcommands}}{{.Name}}{{with .ShortName}}, {{.}}{{end}}{{ "\t" }}{{.Usage}}
	{{end}}{{end}}{{if .Flags}}
OPTIONS:
{{range $.Flags}}{{"\t"}}{{.}}
{{end}}
{{end}}`
)

var (
	// Git SHA1 commit hash of the release (set via linker flags)
	gitCommit = ""

	app *cli.App

	// Flags needed by abigen
	abiFlag = &cli.StringFlag{
		Name:  "abi",
		Usage: "Path to the Klaytn contract ABI json to bind, - for STDIN",
	}
	binFlag = &cli.StringFlag{
		Name:  "bin",
		Usage: "Path to the Klaytn contract bytecode (generate deploy method)",
	}
	binruntimeFlag = &cli.StringFlag{
		Name:  "binruntime",
		Usage: "Path to the GXP contract runtime-bytecode",
	}
	typeFlag = &cli.StringFlag{
		Name:  "type",
		Usage: "Struct name for the binding (default = package name)",
	}
	jsonFlag = &cli.StringFlag{
		Name:  "combined-json",
		Usage: "Path to the combined-json file generated by compiler",
	}
	solFlag = &cli.StringFlag{
		Name:  "sol",
		Usage: "Path to the Klaytn contract Solidity source to build and bind",
	}
	solcFlag = &cli.StringFlag{
		Name:  "solc",
		Usage: "Solidity compiler to use if source builds are requested",
		Value: "solc",
	}
	excFlag = &cli.StringFlag{
		Name:  "exc",
		Usage: "Comma separated types to exclude from binding",
	}
	pkgFlag = &cli.StringFlag{
		Name:  "pkg",
		Usage: "Package name to generate the binding into",
	}
	outFlag = &cli.StringFlag{
		Name:  "out",
		Usage: "Output file for the generated binding (default = stdout)",
	}
	langFlag = &cli.StringFlag{
		Name:  "lang",
		Usage: "Destination language for the bindings (go, java, objc)",
		Value: "go",
	}
	aliasFlag = &cli.StringFlag{
		Name:  "alias",
		Usage: "Comma separated aliases for function and event renaming, e.g. foo=bar",
	}
)

func init() {
	app = utils.NewApp(gitCommit, "klaytn checkpoint helper tool")
	app.Flags = []cli.Flag{
		abiFlag,
		binFlag,
		binruntimeFlag,
		typeFlag,
		jsonFlag,
		solFlag,
		solcFlag,
		excFlag,
		pkgFlag,
		outFlag,
		langFlag,
		aliasFlag,
	}
	app.Action = abigen
	cli.CommandHelpTemplate = commandHelperTemplate
}

func abigen(c *cli.Context) error {
	utils.CheckExclusive(c, abiFlag, jsonFlag, solFlag) // Only one source can be selected.
	if c.String(pkgFlag.Name) == "" {
		log.Fatalf("No destination package specified (--pkg)")
	}
	var lang bind.Lang
	switch c.String(langFlag.Name) {
	case "go":
		lang = bind.LangGo
	case "java":
		lang = bind.LangJava
	case "objc":
		lang = bind.LangObjC
		log.Fatalf("Objc binding generation is uncompleted")
	default:
		log.Fatalf("Unsupported destination language \"%s\" (--lang)", c.String(langFlag.Name))
	}
	// If the entire solidity code was specified, build and bind based on that
	var (
		abis        []string
		bins        []string
		binruntimes []string
		types       []string
		sigs        []map[string]string
		libs        = make(map[string]string)
		aliases     = make(map[string]string)
	)
	if c.String(abiFlag.Name) != "" {
		// Load up the ABI, optional bytecode and type name from the parameters
		var (
			abi []byte
			err error
		)
		input := c.String(abiFlag.Name)
		if input == "-" {
			abi, err = ioutil.ReadAll(os.Stdin)
		} else {
			abi, err = ioutil.ReadFile(input)
		}
		if err != nil {
			log.Fatalf("Failed to read input ABI: %v", err)
		}
		abis = append(abis, string(abi))

		var bin []byte
		if binFile := c.String(binFlag.Name); binFile != "" {
			if bin, err = ioutil.ReadFile(binFile); err != nil {
				log.Fatalf("Failed to read input bytecode: %v", err)
			}
			if strings.Contains(string(bin), "//") {
				log.Fatalf("Contract has additional library references, please use other mode(e.g. --combined-json) to catch library infos")
			}
		}
		bins = append(bins, string(bin))
		var binruntime []byte
		if binruntimeFile := c.String(binruntimeFlag.Name); binruntimeFile != "" {
			if binruntime, err = ioutil.ReadFile(binruntimeFile); err != nil {
				log.Fatalf("Failed to read input runtime-bytecode: %v", err)
			}
			if strings.Contains(string(binruntime), "//") {
				log.Fatalf("Contract has additional library references, please use other ")
			}
		}
		binruntimes = append(binruntimes, string(binruntime))

		kind := c.String(typeFlag.Name)
		if kind == "" {
			kind = c.String(pkgFlag.Name)
		}
		types = append(types, kind)
	} else {
		// Generate the list of types to exclude from binding
		exclude := make(map[string]bool)
		for _, kind := range strings.Split(c.String(excFlag.Name), ",") {
			exclude[strings.ToLower(kind)] = true
		}
		var err error
		var contracts map[string]*compiler.Contract

		switch {
		case c.IsSet(solFlag.Name):
			contracts, err = compiler.CompileSolidity(c.String(solcFlag.Name), c.String(solFlag.Name))
			if err != nil {
				log.Fatalf("Failed to build Solidity contract: %v", err)
			}
		case c.IsSet(jsonFlag.Name):
			jsonOutput, err := ioutil.ReadFile(c.String(jsonFlag.Name))
			if err != nil {
				log.Fatalf("Failed to read combined-json from compiler: %v", err)
			}
			contracts, err = compiler.ParseCombinedJSON(jsonOutput, "", "", "", "")
			if err != nil {
				log.Fatalf("Failed to read contract information from json output: %v", err)
			}
		}
		// Gather all non-excluded contract for binding
		for name, contract := range contracts {
			if exclude[strings.ToLower(name)] {
				continue
			}
			abi, err := json.Marshal(contract.Info.AbiDefinition) // Flatten the compiler parse
			if err != nil {
				log.Fatalf("Failed to parse ABIs from compiler output: %v", err)
			}
			abis = append(abis, string(abi))
			bins = append(bins, contract.Code)
			binruntimes = append(binruntimes, contract.RuntimeCode)
			sigs = append(sigs, contract.Hashes)
			nameParts := strings.Split(name, ":")
			types = append(types, nameParts[len(nameParts)-1])

			libPattern := crypto.Keccak256Hash([]byte(name)).String()[2:36]
			libs[libPattern] = nameParts[len(nameParts)-1]
		}
	}
	// Extract all aliases from the flags
	if c.IsSet(aliasFlag.Name) {
		// We support multi-versions for aliasing
		// e.g.
		//      foo=bar,foo2=bar2
		//      foo:bar,foo2:bar2
		re := regexp.MustCompile(`(?:(\w+)[:=](\w+))`)
		submatches := re.FindAllStringSubmatch(c.String(aliasFlag.Name), -1)
		for _, match := range submatches {
			aliases[match[1]] = match[2]
		}
	}
	// Generate the contract binding
	code, err := bind.Bind(types, abis, bins, binruntimes, sigs, c.String(pkgFlag.Name), lang, libs, aliases)
	if err != nil {
		log.Fatalf("Failed to generate ABI binding: %v", err)
	}
	// Either flush it out to a file or display on the standard output
	if !c.IsSet(outFlag.Name) {
		fmt.Printf("%s\n", code)
		return nil
	}
	if err := ioutil.WriteFile(c.String(outFlag.Name), []byte(code), 0o600); err != nil {
		log.Fatalf("Failed to write ABI binding: %v", err)
	}
	return nil
}

func main() {
	log.Root().SetHandler(log.LvlFilterHandler(log.LvlInfo, log.StreamHandler(os.Stderr, log.TerminalFormat(true))))

	if err := app.Run(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
