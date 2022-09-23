package rpc

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/dontpanicdao/caigo"
	"github.com/dontpanicdao/caigo/rpc/types"
)

const (
	EXECUTE_SELECTOR   string = "__execute__"
	TRANSACTION_PREFIX string = "invoke"
)

type Account struct {
	Provider *Client
	Address  string
	private  *big.Int
}

type ExecuteDetails struct {
	MaxFee  *big.Int
	Nonce   *big.Int
	Version *big.Int
}

func (provider *Client) NewAccount(private, address string) (*Account, error) {
	priv := caigo.SNValToBN(private)

	return &Account{
		Provider: provider,
		Address:  address,
		private:  priv,
	}, nil
}

func (account *Account) Sign(msgHash *big.Int) (*big.Int, *big.Int, error) {
	return caigo.Curve.Sign(msgHash, account.private)
}

func (account *Account) HashMultiCall(calls []types.FunctionCall, details ExecuteDetails) (*big.Int, error) {
	chainID, err := account.Provider.ChainID(context.Background())
	if err != nil {
		return nil, err
	}

	callArray := fmtExecuteCalldata(details.Nonce, calls)
	cdHash, err := caigo.Curve.ComputeHashOnElements(callArray)
	if err != nil {
		return nil, err
	}

	multiHashData := []*big.Int{
		caigo.UTF8StrToBig(TRANSACTION_PREFIX),
		details.Version,
		caigo.SNValToBN(account.Address),
		caigo.GetSelectorFromName(EXECUTE_SELECTOR),
		cdHash,
		details.MaxFee,
		caigo.UTF8StrToBig(chainID),
	}

	return caigo.Curve.ComputeHashOnElements(multiHashData)
}

func (account *Account) Nonce(ctx context.Context) (*big.Int, error) {
	nonce, err := account.Provider.Call(
		ctx,
		types.FunctionCall{
			ContractAddress:    types.HexToHash(account.Address),
			EntryPointSelector: "get_nonce",
			CallData:           []string{},
		},
		WithBlockTag("latest"),
	)
	if err != nil {
		return nil, err
	}
	if len(nonce) == 0 {
		return nil, errors.New("nonce error")
	}
	n, ok := big.NewInt(0).SetString(nonce[0], 0)
	if !ok {
		return nil, errors.New("nonce error")
	}
	return n, nil
}

func (account *Account) EstimateFee(ctx context.Context, calls []types.FunctionCall, details ExecuteDetails) (*types.FeeEstimate, error) {
	var err error
	nonce := details.Nonce
	if details.Nonce == nil {
		nonce, err = account.Nonce(ctx)
		if err != nil {
			return nil, err
		}
	}
	maxFee, _ := big.NewInt(0).SetString("0x200000000", 0)
	if details.MaxFee != nil {
		maxFee = details.MaxFee
	}
	version := big.NewInt(0)
	if details.Version != nil {
		version = details.Version
	}
	txHash, err := account.HashMultiCall(
		calls,
		ExecuteDetails{
			Nonce:   nonce,
			MaxFee:  maxFee,
			Version: version,
		},
	)
	if err != nil {
		return nil, err
	}
	s1, s2, err := account.Sign(txHash)
	if err != nil {
		return nil, err
	}
	calldata := fmtExecuteCalldataStrings(nonce, calls)
	call := types.Call{
		MaxFee:             fmt.Sprintf("0x%s", maxFee.Text(16)),
		Version:            types.NumAsHex(fmt.Sprintf("0x%s", version.Text(16))),
		Signature:          []string{s1.Text(10), s2.Text(10)},
		Nonce:              fmt.Sprintf("0x%s", nonce.Text(16)),
		ContractAddress:    types.HexToHash(account.Address),
		EntryPointSelector: "__execute__",
		CallData:           calldata,
	}
	return account.Provider.EstimateFee(ctx, call, WithBlockTag("latest"))
}

func (account *Account) Execute(ctx context.Context, calls []types.FunctionCall, details ExecuteDetails) (*AddInvokeTransactionOutput, error) {
	var err error
	nonce := details.Nonce
	if details.Nonce == nil {
		nonce, err = account.Nonce(ctx)
		if err != nil {
			return nil, err
		}
	}
	maxFee := details.MaxFee
	if details.MaxFee == nil {
		estimate, err := account.EstimateFee(ctx, calls, details)
		if err != nil {
			return nil, err
		}
		v, ok := big.NewInt(0).SetString(string(estimate.OverallFee), 0)
		if !ok {
			return nil, errors.New("could not match OverallFee to big.Int")
		}
		maxFee = v.Mul(v, big.NewInt(2))
	}
	version := big.NewInt(0)
	if details.Version != nil {
		version = details.Version
	}
	txHash, err := account.HashMultiCall(
		calls,
		ExecuteDetails{
			Nonce:   nonce,
			MaxFee:  maxFee,
			Version: version,
		},
	)
	if err != nil {
		return nil, err
	}
	s1, s2, err := account.Sign(txHash)
	if err != nil {
		return nil, err
	}
	calldata := fmtExecuteCalldataStrings(nonce, calls)
	return account.Provider.AddInvokeTransaction(
		context.Background(),
		types.FunctionCall{
			ContractAddress:    types.HexToHash(account.Address),
			EntryPointSelector: "__execute__",
			CallData:           calldata,
		},
		[]string{s1.Text(10), s2.Text(10)},
		fmt.Sprintf("0x%s", maxFee.Text(16)),
		fmt.Sprintf("0x%s", version.Text(16)),
	)
}

func fmtExecuteCalldataStrings(nonce *big.Int, calls []types.FunctionCall) (calldataStrings []string) {
	callArray := fmtExecuteCalldata(nonce, calls)
	for _, data := range callArray {
		calldataStrings = append(calldataStrings, fmt.Sprintf("0x%s", data.Text(16)))
	}
	return calldataStrings
}

/*
Formats the multicall transactions in a format which can be signed and verified by the network and OpenZeppelin account contracts
*/
func fmtExecuteCalldata(nonce *big.Int, calls []types.FunctionCall) (calldataArray []*big.Int) {
	callArray := []*big.Int{big.NewInt(int64(len(calls)))}

	for _, tx := range calls {
		address, _ := big.NewInt(0).SetString(tx.ContractAddress.Hex(), 0)
		callArray = append(callArray, address, caigo.GetSelectorFromName(tx.EntryPointSelector))

		if len(tx.CallData) == 0 {
			callArray = append(callArray, big.NewInt(0), big.NewInt(0))

			continue
		}

		callArray = append(callArray, big.NewInt(int64(len(calldataArray))), big.NewInt(int64(len(tx.CallData))))
		for _, cd := range tx.CallData {
			calldataArray = append(calldataArray, caigo.SNValToBN(cd))
		}
	}

	callArray = append(callArray, big.NewInt(int64(len(calldataArray))))
	callArray = append(callArray, calldataArray...)
	callArray = append(callArray, nonce)
	return callArray
}