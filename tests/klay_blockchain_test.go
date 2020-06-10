// Copyright 2020 The klaytn Authors
// This file is part of the klaytn library.
//
// The klaytn library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The klaytn library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the klaytn library. If not, see <http://www.gnu.org/licenses/>.

package tests

import (
	"crypto/ecdsa"
	"github.com/klaytn/klaytn/blockchain"
	"github.com/klaytn/klaytn/blockchain/types"
	"github.com/klaytn/klaytn/common"
	"github.com/klaytn/klaytn/consensus/istanbul"
	"github.com/klaytn/klaytn/crypto"
	"github.com/klaytn/klaytn/networks/p2p"
	"github.com/klaytn/klaytn/node"
	"github.com/klaytn/klaytn/node/cn"
	"github.com/klaytn/klaytn/params"
	"github.com/klaytn/klaytn/ser/rlp"
	"github.com/klaytn/klaytn/work"
	"io/ioutil"
	"math/big"
	"os"
	"testing"
	"time"
)

// TestSimpleBlockchain
func TestSimpleBlockchain(t *testing.T) {
	if testing.Verbose() {
		enableLog() // Change verbosity level in the function if needed
	}

	numAccounts := 12
	fullNode, node, validator, chainId, workspace := newBlockchain(t)

	// create account
	richAccount, accounts, contractAccounts := createAccount(t, numAccounts, validator)
	contractDeployCode := "0x608060405234801561001057600080fd5b506000808190555060646001819055506101848061002f6000396000f300608060405260043610610062576000357c0100000000000000000000000000000000000000000000000000000000900463ffffffff16806302e5329e14610067578063197e70e41461009457806349b667d2146100c157806367e0badb146100ec575b600080fd5b34801561007357600080fd5b5061009260048036038101908080359060200190929190505050610117565b005b3480156100a057600080fd5b506100bf60048036038101908080359060200190929190505050610121565b005b3480156100cd57600080fd5b506100d6610145565b6040518082815260200191505060405180910390f35b3480156100f857600080fd5b5061010161014f565b6040518082815260200191505060405180910390f35b8060018190555050565b806000540160008190555060015460005481151561013b57fe5b0660008190555050565b6000600154905090565b600080549050905600a165627a7a72305820ef4e7e564c744de3a36cb74000c35687f7de9ecf1d29abdd3c4bcc66db981c160029"
	for i := 0; i < numAccounts; i++ {
		contractAccounts[i].Addr = deployContractDeployTx(t, node.TxPool(), chainId, richAccount, contractDeployCode)
	}

	// deploy
	for i := 0; i < numAccounts; i++ {
		deployRandomTxs(t, node.TxPool(), chainId, richAccount, 10)
		deployValueTransferTx(t, node.TxPool(), chainId, richAccount, accounts[i%numAccounts])
		deployContractExecutionTx(t, node.TxPool(), chainId, richAccount, contractAccounts[i%numAccounts].Addr)
	}

	// stop full node
	if err := fullNode.Stop(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Second)

	// start full node with previous db
	newKlaytnNode(t, workspace, validator)
	if err := node.StartMining(false); err != nil {
		t.Fatal()
	}
	time.Sleep(5 * time.Second)
}

func newBlockchain(t *testing.T) (*node.Node, *cn.CN, *TestAccountType, *big.Int, string) {
	t.Log("Create a new blockchain")
	// Prepare workspace
	workspace, err := ioutil.TempDir("", "klaytn-test-state")
	if err != nil {
		t.Fatalf("failed to create temporary keystore: %v", err)
	}
	defer os.RemoveAll(workspace)
	t.Log("Workspace is ", workspace)

	// Prepare a validator
	validator, err := createAnonymousAccount(getRandomPrivateKeyString(t))
	if err != nil {
		t.Fatal(err)
	}

	// Create a Klaytn node
	fullNode, node := newKlaytnNode(t, workspace, validator)
	if err := node.StartMining(false); err != nil {
		t.Fatal()
	}
	time.Sleep(2 * time.Second) // wait for initializing mining

	chainId := node.BlockChain().Config().ChainID

	return fullNode, node, validator, chainId, workspace
}

func createAccount(t *testing.T, numAccounts int, validator *TestAccountType) (*TestAccountType, []*TestAccountType, []*TestAccountType) {
	accounts := make([]*TestAccountType, numAccounts)
	contractAccounts := make([]*TestAccountType, numAccounts)

	// richAccount is used for deploying smart contracts
	richAccount := &TestAccountType{
		Addr:  validator.Addr,
		Keys:  []*ecdsa.PrivateKey{validator.Keys[0]},
		Nonce: uint64(0),
	}

	var err error
	for i := 0; i < numAccounts; i++ {
		if accounts[i], err = createAnonymousAccount(getRandomPrivateKeyString(t)); err != nil {
			t.Fatal()
		}
		// address should be overwritten
		if contractAccounts[i], err = createAnonymousAccount(getRandomPrivateKeyString(t)); err != nil {
			t.Fatal()
		}
	}

	return richAccount, accounts, contractAccounts
}

// newKlaytnNode creates a klaytn node
func newKlaytnNode(t *testing.T, dir string, validator *TestAccountType) (*node.Node, *cn.CN) {
	var klaytnNode *cn.CN

	fullNode, err := node.New(&node.Config{DataDir: dir, UseLightweightKDF: true, P2P: p2p.Config{PrivateKey: validator.Keys[0]}})
	if err != nil {
		t.Fatalf("failed to create node: %v", err)
	}

	istanbulConfData, err := rlp.EncodeToBytes(&types.IstanbulExtra{
		Validators:    []common.Address{validator.Addr},
		Seal:          []byte{},
		CommittedSeal: [][]byte{},
	})
	if err != nil {
		t.Fatal(err)
	}

	genesis := blockchain.DefaultGenesisBlock()
	genesis.ExtraData = genesis.ExtraData[:types.IstanbulExtraVanity]
	genesis.ExtraData = append(genesis.ExtraData, istanbulConfData...)
	genesis.Config.Istanbul.SubGroupSize = 1
	genesis.Config.Istanbul.ProposerPolicy = uint64(istanbul.RoundRobin)
	genesis.Config.Governance.Reward.MintingAmount = new(big.Int).Mul(big.NewInt(100), big.NewInt(params.KLAY))

	cnConf := &cn.DefaultConfig
	cnConf.Genesis = genesis
	cnConf.Rewardbase = validator.Addr
	cnConf.PartitionedDB = true
	cnConf.NumStateTriePartitions = 4

	if err = fullNode.Register(func(ctx *node.ServiceContext) (node.Service, error) { return cn.New(ctx, cnConf) }); err != nil {
		t.Fatalf("failed to register Klaytn protocol: %v", err)
	}

	if err = fullNode.Start(); err != nil {
		t.Fatalf("failed to start test fullNode: %v", err)
	}

	if err := fullNode.Service(&klaytnNode); err != nil {
		t.Fatal(err)
	}

	return fullNode, klaytnNode
}

// deployRandomTxs creates a random transaction
func deployRandomTxs(t *testing.T, txpool work.TxPool, chainId *big.Int, sender *TestAccountType, txNum int) {
	var tx *types.Transaction
	signer := types.NewEIP155Signer(chainId)
	gasPrice := big.NewInt(25 * params.Ston)
	gasLimit = uint64(100000)

	txNuminABlock := 100
	for i := 0; i < txNum; i++ {
		if i%txNuminABlock == 0 {
			time.Sleep(time.Second)
		}

		receiver, err := createAnonymousAccount(getRandomPrivateKeyString(t))
		if err != nil {
			t.Fatal()
		}

		tx, _ = genLegacyTransaction(t, signer, sender, receiver, nil, gasPrice)
		if err := txpool.AddLocal(tx); err != nil && err != blockchain.ErrAlreadyNonceExistInPool {
			t.Fatal(err)
		}
		sender.AddNonce()
	}
}

// deployValueTransferTx deploy value transfer transactions
func deployValueTransferTx(t *testing.T, txpool work.TxPool, chainId *big.Int, sender *TestAccountType, toAcc *TestAccountType) {
	signer := types.NewEIP155Signer(chainId)
	gasPrice := big.NewInt(25 * params.Ston)

	tx, _ := genLegacyTransaction(t, signer, sender, toAcc, nil, gasPrice)
	if err := txpool.AddLocal(tx); err != nil && err != blockchain.ErrAlreadyNonceExistInPool {
		t.Fatal(err)
	}
	sender.AddNonce()
}

// deployContractDeployTx deploy contrac
func deployContractDeployTx(t *testing.T, txpool work.TxPool, chainId *big.Int, sender *TestAccountType, contractDeployCode string) common.Address {
	signer := types.NewEIP155Signer(chainId)
	gasPrice := big.NewInt(25 * params.Ston)

	values := map[types.TxValueKeyType]interface{}{
		types.TxValueKeyNonce:         sender.GetNonce(),
		types.TxValueKeyAmount:        new(big.Int).SetUint64(0),
		types.TxValueKeyGasLimit:      uint64(1000000),
		types.TxValueKeyGasPrice:      gasPrice,
		types.TxValueKeyHumanReadable: false,
		types.TxValueKeyFrom:          sender.GetAddr(),
		types.TxValueKeyData:          common.FromHex(contractDeployCode),
		types.TxValueKeyCodeFormat:    params.CodeFormatEVM,
		types.TxValueKeyTo:            (*common.Address)(nil),
	}
	tx, err := types.NewTransactionWithMap(types.TxTypeSmartContractDeploy, values)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.SignWithKeys(signer, sender.GetTxKeys()); err != nil {
		t.Fatal(err)
	}
	if err := txpool.AddLocal(tx); err != nil && err != blockchain.ErrAlreadyNonceExistInPool {
		t.Fatal(err)
	}
	contractAddr := crypto.CreateAddress(sender.Addr, sender.Nonce)
	sender.AddNonce()

	return contractAddr
}

func deployContractExecutionTx(t *testing.T, txpool work.TxPool, chainId *big.Int, sender *TestAccountType, contractAddr common.Address) {
	signer := types.NewEIP155Signer(chainId)
	gasPrice := big.NewInt(25 * params.Ston)
	contractExecutionPayload := "0x197e70e40000000000000000000000000000000000000000000000000000000000000001"

	values := map[types.TxValueKeyType]interface{}{
		types.TxValueKeyNonce:    sender.GetNonce(),
		types.TxValueKeyFrom:     sender.GetAddr(),
		types.TxValueKeyTo:       contractAddr,
		types.TxValueKeyAmount:   new(big.Int).SetUint64(0),
		types.TxValueKeyGasLimit: gasLimit,
		types.TxValueKeyGasPrice: gasPrice,
		types.TxValueKeyData:     common.FromHex(contractExecutionPayload),
	}
	tx, err := types.NewTransactionWithMap(types.TxTypeSmartContractExecution, values)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.SignWithKeys(signer, sender.GetTxKeys()); err != nil {
		t.Fatal(err)
	}
	if err := txpool.AddLocal(tx); err != nil && err != blockchain.ErrAlreadyNonceExistInPool {
		t.Fatal(err)
	}
	sender.AddNonce()
}
