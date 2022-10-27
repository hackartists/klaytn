package governance

import (
	"errors"
	"math/big"

	"github.com/klaytn/klaytn/common"
	"github.com/klaytn/klaytn/params"
)

var errContractEngineNotReady = errors.New("ContractEngine is not ready")

type ContractEngine struct {
	chainConfig   *params.ChainConfig
	currentParams *params.GovParamSet

	chain blockChain // To access the contract state DB
}

func NewContractEngine(config *params.ChainConfig) *ContractEngine {
	e := &ContractEngine{
		chainConfig:   config,
		currentParams: params.NewGovParamSet(),
	}

	return e
}

func (e *ContractEngine) SetBlockchain(chain blockChain) {
	e.chain = chain
}

// Params effective at upcoming block (head+1)
func (e *ContractEngine) Params() *params.GovParamSet {
	return e.currentParams
}

// Params effective at requested block (num)
func (e *ContractEngine) ParamsAt(num uint64) (*params.GovParamSet, error) {
	if e.chain == nil {
		logger.Error("Invoked ParamsAt() before SetBlockchain", "num", num)
		return nil, errContractEngineNotReady
	}

	head := e.chain.CurrentHeader().Number.Uint64()
	if num > head {
		// Sometimes future blocks are requested.
		// ex) reward distributor in istanbul.engine.Finalize() requests ParamsAt(head+1)
		// ex) governance_itemsAt(num) API requests arbitrary num
		// In those cases we refer to the head block.
		num = head + 1
	}

	pset, err := e.contractGetAllParamsAt(num)
	if err != nil {
		return nil, err
	}
	return pset, nil
}

// if UpdateParam fails, leave currentParams as-is
func (e *ContractEngine) UpdateParams() error {
	if e.chain == nil {
		logger.Error("Invoked UpdateParams() before SetBlockchain")
		return errContractEngineNotReady
	}

	// request the parameters required for generating the next block
	head := e.chain.CurrentHeader().Number.Uint64()
	pset, err := e.contractGetAllParamsAt(head + 1)
	if err != nil {
		return err
	}

	e.currentParams = pset
	return nil
}

// contractGetAllParamsAt sets evmCtx.BlockNumber as num
func (e *ContractEngine) contractGetAllParamsAt(num uint64) (*params.GovParamSet, error) {
	if e.chain == nil {
		logger.Error("Invoked ContractEngine before SetBlockchain")
		return nil, errContractEngineNotReady
	}

	addr := e.contractAddrAt(num)
	if common.EmptyAddress(addr) {
		logger.Error("Invoked ContractEngine but address is empty", "num", num)
		return nil, errContractEngineNotReady
	}

	caller := &contractCaller{
		chainConfig:  e.chainConfig,
		chain:        e.chain,
		contractAddr: addr,
	}
	return caller.getAllParamsAt(new(big.Int).SetUint64(num))
}

// Return the GovParamContract address effective at given block number
func (e *ContractEngine) contractAddrAt(num uint64) common.Address {
	// TODO: Load from HeaderEngine

	// If database don't have the item, fallback to ChainConfig
	if e.chainConfig.Governance != nil {
		return e.chainConfig.Governance.GovParamContract
	}

	return common.Address{}
}
