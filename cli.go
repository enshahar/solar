package solar

import (
	"encoding/hex"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/pkg/errors"
	"github.com/enshahar/solar/contract"
	"github.com/enshahar/solar/deployer"
	"github.com/enshahar/solar/deployer/eth"
	"github.com/enshahar/solar/deployer/qtum"
	"github.com/enshahar/solar/varstr"
	kingpin "gopkg.in/alecthomas/kingpin.v2"
)

var (
	app     = kingpin.New("solar", "Solidity smart contract deployment management.")
	qtumRPC = app.Flag("qtum_rpc", "RPC provider url").Envar("QTUM_RPC").String()
	qtumSenderAddress = app.Flag("qtum_sender", "(qtum) Sender UXTO Address").Envar("QTUM_SENDER").String()

	// geth --rpc --rpcapi="eth,personal,miner"
	ethRPC    = app.Flag("eth_rpc", "RPC provider url").Envar("ETH_RPC").String()
	solarEnv  = app.Flag("env", "Environment name").Envar("SOLAR_ENV").Default("development").String()
	solarRepo = app.Flag("repo", "Path of contracts repository").Envar("SOLAR_REPO").String()
	appTasks  = map[string]func() error{}

	solcOptimize   = app.Flag("optimize", "[solc] should Enable bytecode optimizer").Default("true").Bool()
	solcAllowPaths = app.Flag("allow-paths", "[solc] Allow a given path for imports. A list of paths can be supplied by separating them with a comma.").Default("").String()
)

type solarCLI struct {
	depoyer      deployer.Deployer
	deployerOnce sync.Once

	repo     *contract.ContractsRepository
	repoOnce sync.Once

	reporter     *events
	reporterOnce sync.Once
}

var solar = &solarCLI{}

func (c *solarCLI) Reporter() *events {
	c.reporterOnce.Do(func() {
		c.reporter = &events{
			in: make(chan interface{}),
		}

		go c.reporter.Start()
	})

	return c.reporter
}

func (c *solarCLI) SolcOptions() (*CompilerOptions, error) {
	allowPathsStr := *solcAllowPaths
	if allowPathsStr == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, errors.Wrap(err, "solc options")
		}

		allowPathsStr = cwd
	}

	allowPaths := strings.Split(allowPathsStr, ",")

	return &CompilerOptions{
		NoOptimize: !*solcOptimize,
		AllowPaths: allowPaths,
	}, nil
}

// Open the file `solar.{SOLAR_ENV}.json` as contracts repository
func (c *solarCLI) ContractsRepository() *contract.ContractsRepository {
	c.repoOnce.Do(func() {
		var repoFilePath string
		if *solarRepo != "" {
			repoFilePath = *solarRepo
		} else {
			repoFilePath = fmt.Sprintf("solar.%s.json", *solarEnv)
		}

		repo, err := contract.OpenContractsRepository(repoFilePath)
		if err != nil {
			fmt.Println("Cannot open contracts repo:", repoFilePath)
			os.Exit(1)
		}

		c.repo = repo
	})

	return c.repo
}

func (c *solarCLI) QtumRPC() *qtum.RPC {
	rpc, err := qtum.NewRPC(*qtumRPC)
	if err != nil {
		fmt.Println("Invalid QTUM RPC URL:", *qtumRPC)
		os.Exit(1)
	}

	return rpc
}

// ExpandJSONParams uses variable substitution syntax (e.g. $Foo, ${Foo}) to as placeholder for contract addresses
func (c *solarCLI) ExpandJSONParams(jsonParams string) string {
	repo := c.ContractsRepository()

	return varstr.Expand(jsonParams, func(key string) string {
		contract, found := repo.Get(key)
		if !found {
			panic(errors.Errorf("Invalid address expansion: %s", key))
		}

		return fmt.Sprintf("%#v", hex.EncodeToString(contract.Address))
	})
}

func (c *solarCLI) Deployer() (deployer deployer.Deployer) {
	log := log.New(os.Stderr, "", log.Lshortfile)

	var err error
	var rpcURL *url.URL
	rawurl := *qtumRPC
	if rawurl != "" {
		rpcURL, err = url.ParseRequestURI(rawurl)
		if err != nil {
			log.Fatalf("Invalid RPC url: %#v", rawurl)
		}
		deployer, err = qtum.NewDeployer(rpcURL, c.ContractsRepository(), *qtumSenderAddress)
	} else if rawurl = *ethRPC; rawurl != "" {
		rpcURL, err = url.ParseRequestURI(rawurl)
		if err != nil {
			log.Fatalf("Invalid RPC url: %#v", rawurl)
		}
		deployer, err = eth.NewDeployer(rpcURL, c.ContractsRepository())
	}

	if err != nil {
		log.Fatalf("NewDeployer error %v", err)
	}

	if rawurl == "" {
		log.Fatalln("Please specify RPC url by setting QTUM_RPC or ETH_RPC or using flag --qtum_rpc or --eth_rpc")
	}

	return deployer
}

func Main() {
	cmdName, err := app.Parse(os.Args[1:])
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	task := appTasks[cmdName]
	err = task()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
