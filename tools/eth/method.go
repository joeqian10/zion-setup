/*
* Copyright (C) 2020 The poly network Authors
* This file is part of The poly network library.
*
* The poly network is free software: you can redistribute it and/or modify
* it under the terms of the GNU Lesser General Public License as published by
* the Free Software Foundation, either version 3 of the License, or
* (at your option) any later version.
*
* The poly network is distributed in the hope that it will be useful,
* but WITHOUT ANY WARRANTY; without even the implied warranty of
* MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
* GNU Lesser General Public License for more details.
* You should have received a copy of the GNU Lesser General Public License
* along with The poly network . If not, see <http://www.gnu.org/licenses/>.
 */
package eth

import (
	"context"
	"fmt"
	"github.com/KSlashh/poly-abi/abi_1.9.25/ccm"
	"github.com/ethereum/go-ethereum/common"
	"github.com/polynetwork/zion-setup/tools/zion"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/contracts/native/go_abi/header_sync_abi"
	"github.com/ethereum/go-ethereum/contracts/native/utils"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/polynetwork/zion-setup/config"
	"github.com/polynetwork/zion-setup/log"
)

func SyncETHToZion(z *zion.ZionTools, e *ETHTools, signerArr []*zion.ZionSigner) {
	curr, err := e.GetNodeHeight()
	if err != nil {
		panic(err)
	}
	hdr, err := e.GetBlockHeader(curr)
	if err != nil {
		panic(err)
	}
	raw, err := hdr.MarshalJSON()
	if err != nil {
		panic(err)
	}

	scmAbi, err := abi.JSON(strings.NewReader(header_sync_abi.HeaderSyncABI))
	if err != nil {
		panic(fmt.Errorf("SyncETHToZion, abi.JSON error:" + err.Error()))
	}
	gasPrice, err := z.GetEthClient().SuggestGasPrice(context.Background())
	if err != nil {
		panic(fmt.Errorf("SyncETHToZion, get suggest gas price failed error: %s", err.Error()))
	}
	gasPrice = gasPrice.Mul(gasPrice, big.NewInt(1))

	txData, err := scmAbi.Pack("syncGenesisHeader", config.DefConfig.ETHConfig.ChainId, raw)
	if err != nil {
		panic(fmt.Errorf("SyncETHToZion, scmAbi.Pack error:" + err.Error()))
	}

	for _, signer := range signerArr {
		callMsg := ethereum.CallMsg{
			From: signer.Address, To: &utils.HeaderSyncContractAddress, Gas: 0, GasPrice: gasPrice,
			Value: big.NewInt(int64(0)), Data: txData,
		}
		gasLimit, err := z.GetEthClient().EstimateGas(context.Background(), callMsg)
		if err != nil {
			panic(fmt.Errorf("SyncETHToZion, estimate gas limit error: %s", err.Error()))
		}
		nonce := NewNonceManager(z.GetEthClient()).GetAddressNonce(signer.Address)
		tx := types.NewTx(&types.LegacyTx{Nonce: nonce, GasPrice: gasPrice, Gas: gasLimit, To: &utils.HeaderSyncContractAddress, Value: big.NewInt(0), Data: txData})
		chainID, err := z.GetChainID()
		if err != nil {
			panic(fmt.Errorf("SyncETHToZion, get chain id error: %s", err.Error()))
		}
		signedtx, err := types.SignTx(tx, types.NewEIP155Signer(chainID), signer.PrivateKey)
		if err != nil {
			panic(fmt.Errorf("SignTransaction failed:%v", err))
		}
		duration := time.Second * 20
		timerCtx, cancelFunc := context.WithTimeout(context.Background(), duration)
		defer cancelFunc()
		err = z.GetEthClient().SendTransaction(timerCtx, signedtx)
		if err != nil {
			panic(fmt.Errorf("SendTransaction failed:%v", err))
		}
		txhash := signedtx.Hash()
		isSuccess := z.WaitTransactionConfirm(txhash)
		if isSuccess {
			log.Infof("successful sync eth genesis header to zion: (poly_hash: %s, account: %s)", txhash.String(), signer.Address.Hex())
		} else {
			log.Errorf("failed to sync eth genesis header to zion: (poly_hash: %s, account: %s)", txhash.String(), signer.Address.Hex())
		}
	}
}

func SyncZionToETH(z *zion.ZionTools, e *ETHTools) {
	signer, err := NewEthSigner(config.DefConfig.ETHConfig.ETHPrivateKey)
	if err != nil {
		panic(err)
	}
	curr, err := z.GetNodeHeight()
	if err != nil {
		panic(err)
	}
	epochInfo, err := z.GetEpochInfo()
	if err != nil {
		panic(fmt.Errorf("SyncZionToETH, GetEpochInfo error: %s", err.Error()))
	}
	rawEpochInfo, err := zion.GetRawEpochInfo(epochInfo.ID, epochInfo.StartHeight, epochInfo.Peers)
	if err != nil {
		panic(fmt.Errorf("SyncZionToETH, GetRawEpochInfo error: %s", err.Error()))
	}
	rawHeader, rawSeals, err := z.GetRawHeaderAndRawSeals(curr)
	if err != nil {
		panic(fmt.Errorf("SyncZionToETH, GetRawHeaderAndRawSeals error: %s", err.Error()))
	}
	epochProofIndex := zion.GetEpochKey(epochInfo.ID)
	accountProof, storageProof, err := z.GetRawProof(utils.NodeManagerContractAddress.String(), epochProofIndex.String(), curr)
	if err != nil {
		panic(fmt.Errorf("SyncZionToETH, GetRawProof error: %s", err.Error()))
	}

	contractabi, err := abi.JSON(strings.NewReader(ccm.EthCrossChainManagerImplemetationABI))
	if err != nil {
		log.Errorf("SyncZionToETH, abi.JSON error: %v", err)
		return
	}
	txData, err := contractabi.Pack("initGenesisBlock", rawHeader, rawSeals, accountProof, storageProof, rawEpochInfo)
	if err != nil {
		log.Errorf("SyncZionToETH, contractabi.Pack error: %v", err)
		return
	}

	gasPrice, err := e.GetEthClient().SuggestGasPrice(context.Background())
	if err != nil {
		panic(fmt.Errorf("SyncZionToETH, get suggest gas price failed error: %s", err.Error()))
	}
	gasPrice = gasPrice.Mul(gasPrice, big.NewInt(1))
	eccm := common.HexToAddress(config.DefConfig.ETHConfig.Eccm)
	callMsg := ethereum.CallMsg{
		From: signer.Address, To: &eccm, Gas: 0, GasPrice: gasPrice,
		Value: big.NewInt(0), Data: txData,
	}
	gasLimit, err := e.GetEthClient().EstimateGas(context.Background(), callMsg)
	if err != nil {
		log.Errorf("SyncZionToETH, estimate gas limit error: %s", err.Error())
		return
	}
	nonce := NewNonceManager(e.GetEthClient()).GetAddressNonce(signer.Address)
	tx := types.NewTx(&types.LegacyTx{Nonce: nonce, GasPrice: gasPrice, Gas: gasLimit, To: &eccm, Value: big.NewInt(0), Data: txData})
	chainID, err := e.GetChainID()
	if err != nil {
		panic(fmt.Errorf("SyncZionToETH, get chain id error: %s", err.Error()))
	}
	signedtx, err := types.SignTx(tx, types.NewEIP155Signer(chainID), signer.PrivateKey)
	if err != nil {
		panic(fmt.Errorf("SignTransaction failed:%v", err))
	}
	duration := time.Second * 20
	timerCtx, cancelFunc := context.WithTimeout(context.Background(), duration)
	defer cancelFunc()
	err = e.GetEthClient().SendTransaction(timerCtx, signedtx)
	if err != nil {
		panic(fmt.Errorf("SendTransaction failed:%v", err))
	}
	e.WaitTransactionConfirm(signedtx.Hash())
	log.Infof("successful to sync zion genesis header to Ethereum: ( txhash: %s )", signedtx.Hash().String())
}