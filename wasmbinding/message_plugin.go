package wasmbinding

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"golang.org/x/exp/maps"

	dexkeeper "github.com/neutron-org/neutron/v2/x/dex/keeper"
	dextypes "github.com/neutron-org/neutron/v2/x/dex/types"

	contractmanagerkeeper "github.com/neutron-org/neutron/v2/x/contractmanager/keeper"

	"cosmossdk.io/errors"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	crontypes "github.com/neutron-org/neutron/v2/x/cron/types"

	cronkeeper "github.com/neutron-org/neutron/v2/x/cron/keeper"

	paramChange "github.com/cosmos/cosmos-sdk/x/params/types/proposal"

	wasmkeeper "github.com/CosmWasm/wasmd/x/wasm/keeper"
	wasmvmtypes "github.com/CosmWasm/wasmvm/types"
	"github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	softwareUpgrade "github.com/cosmos/cosmos-sdk/x/upgrade/types"

	adminmodulekeeper "github.com/cosmos/admin-module/x/adminmodule/keeper"
	admintypes "github.com/cosmos/admin-module/x/adminmodule/types"

	bankkeeper "github.com/cosmos/cosmos-sdk/x/bank/keeper"
	ibcclienttypes "github.com/cosmos/ibc-go/v7/modules/core/02-client/types"

	"github.com/neutron-org/neutron/v2/wasmbinding/bindings"
	icqkeeper "github.com/neutron-org/neutron/v2/x/interchainqueries/keeper"
	icqtypes "github.com/neutron-org/neutron/v2/x/interchainqueries/types"
	ictxkeeper "github.com/neutron-org/neutron/v2/x/interchaintxs/keeper"
	ictxtypes "github.com/neutron-org/neutron/v2/x/interchaintxs/types"
	transferwrapperkeeper "github.com/neutron-org/neutron/v2/x/transfer/keeper"
	transferwrappertypes "github.com/neutron-org/neutron/v2/x/transfer/types"

	tokenfactorykeeper "github.com/neutron-org/neutron/v2/x/tokenfactory/keeper"
	tokenfactorytypes "github.com/neutron-org/neutron/v2/x/tokenfactory/types"
)

func CustomMessageDecorator(
	ictx *ictxkeeper.Keeper,
	icq *icqkeeper.Keeper,
	transferKeeper transferwrapperkeeper.KeeperTransferWrapper,
	adminKeeper *adminmodulekeeper.Keeper,
	bankKeeper *bankkeeper.BaseKeeper,
	tokenFactoryKeeper *tokenfactorykeeper.Keeper,
	cronKeeper *cronkeeper.Keeper,
	contractmanagerKeeper *contractmanagerkeeper.Keeper,
	dexKeeper *dexkeeper.Keeper,
) func(messenger wasmkeeper.Messenger) wasmkeeper.Messenger {
	return func(old wasmkeeper.Messenger) wasmkeeper.Messenger {
		return &CustomMessenger{
			Keeper:                *ictx,
			Wrapped:               old,
			Ictxmsgserver:         ictxkeeper.NewMsgServerImpl(*ictx),
			Icqmsgserver:          icqkeeper.NewMsgServerImpl(*icq),
			transferKeeper:        transferKeeper,
			Adminserver:           adminmodulekeeper.NewMsgServerImpl(*adminKeeper),
			Bank:                  bankKeeper,
			TokenFactory:          tokenFactoryKeeper,
			CronKeeper:            cronKeeper,
			AdminKeeper:           adminKeeper,
			ContractmanagerKeeper: contractmanagerKeeper,
			DexMsgServer:          dexkeeper.NewMsgServerImpl(*dexKeeper),
		}
	}
}

type CustomMessenger struct {
	Keeper                ictxkeeper.Keeper
	Wrapped               wasmkeeper.Messenger
	Ictxmsgserver         ictxtypes.MsgServer
	Icqmsgserver          icqtypes.MsgServer
	transferKeeper        transferwrapperkeeper.KeeperTransferWrapper
	Adminserver           admintypes.MsgServer
	Bank                  *bankkeeper.BaseKeeper
	TokenFactory          *tokenfactorykeeper.Keeper
	CronKeeper            *cronkeeper.Keeper
	AdminKeeper           *adminmodulekeeper.Keeper
	ContractmanagerKeeper *contractmanagerkeeper.Keeper
	DexMsgServer          dextypes.MsgServer
}

var _ wasmkeeper.Messenger = (*CustomMessenger)(nil)

func (m *CustomMessenger) DispatchMsg(ctx sdk.Context, contractAddr sdk.AccAddress, contractIBCPortID string, msg wasmvmtypes.CosmosMsg) ([]sdk.Event, [][]byte, error) {
	// Return early if msg.Custom is nil
	if msg.Custom == nil {
		return m.Wrapped.DispatchMsg(ctx, contractAddr, contractIBCPortID, msg)
	}

	var contractMsg bindings.NeutronMsg
	if err := json.Unmarshal(msg.Custom, &contractMsg); err != nil {
		ctx.Logger().Debug("json.Unmarshal: failed to decode incoming custom cosmos message",
			"from_address", contractAddr.String(),
			"message", string(msg.Custom),
			"error", err,
		)
		return nil, nil, errors.Wrap(err, "failed to decode incoming custom cosmos message")
	}

	// Dispatch the message based on its type by checking each possible field
	if contractMsg.SubmitTx != nil {
		return m.submitTx(ctx, contractAddr, contractMsg.SubmitTx)
	}
	if contractMsg.RegisterInterchainAccount != nil {
		return m.registerInterchainAccount(ctx, contractAddr, contractMsg.RegisterInterchainAccount)
	}
	if contractMsg.RegisterInterchainQuery != nil {
		return m.registerInterchainQuery(ctx, contractAddr, contractMsg.RegisterInterchainQuery)
	}
	if contractMsg.UpdateInterchainQuery != nil {
		return m.updateInterchainQuery(ctx, contractAddr, contractMsg.UpdateInterchainQuery)
	}
	if contractMsg.RemoveInterchainQuery != nil {
		return m.removeInterchainQuery(ctx, contractAddr, contractMsg.RemoveInterchainQuery)
	}
	if contractMsg.IBCTransfer != nil {
		return m.ibcTransfer(ctx, contractAddr, *contractMsg.IBCTransfer)
	}
	if contractMsg.SubmitAdminProposal != nil {
		return m.submitAdminProposal(ctx, contractAddr, &contractMsg.SubmitAdminProposal.AdminProposal)
	}
	if contractMsg.CreateDenom != nil {
		return m.createDenom(ctx, contractAddr, contractMsg.CreateDenom)
	}
	if contractMsg.MintTokens != nil {
		return m.mintTokens(ctx, contractAddr, contractMsg.MintTokens)
	}
	if contractMsg.SetBeforeSendHook != nil {
		return m.setBeforeSendHook(ctx, contractAddr, contractMsg.SetBeforeSendHook)
	}
	if contractMsg.ChangeAdmin != nil {
		return m.changeAdmin(ctx, contractAddr, contractMsg.ChangeAdmin)
	}
	if contractMsg.BurnTokens != nil {
		return m.burnTokens(ctx, contractAddr, contractMsg.BurnTokens)
	}
	if contractMsg.AddSchedule != nil {
		return m.addSchedule(ctx, contractAddr, contractMsg.AddSchedule)
	}
	if contractMsg.RemoveSchedule != nil {
		return m.removeSchedule(ctx, contractAddr, contractMsg.RemoveSchedule)
	}
	if contractMsg.ResubmitFailure != nil {
		return m.resubmitFailure(ctx, contractAddr, contractMsg.ResubmitFailure)
	}
	if contractMsg.Dex != nil {
		data, err := m.dispatchDexMsg(ctx, contractAddr, *(contractMsg.Dex))
		return nil, data, err
	}

	// If none of the conditions are met, forward the message to the wrapped handler
	return m.Wrapped.DispatchMsg(ctx, contractAddr, contractIBCPortID, msg)
}

// TODO: add handler name as arg for logging purpose
// fmt.Sprintf("%T: failed to execute", handler) -> fmt.Sprintf("%Ts: failed to execute", handlerName)
func handleDexMsg[T sdk.Msg, R any](ctx sdk.Context, msg T, handler func(ctx context.Context, msg T) (R, error)) ([][]byte, error) {
	if err := msg.ValidateBasic(); err != nil {
		return nil, errors.Wrapf(err, "failed to validate %T", msg)
	}

	resp, err := handler(ctx, msg)
	if err != nil {
		ctx.Logger().Debug(fmt.Sprintf("%T: failed to execute", msg),
			"from_address", msg.GetSigners()[0].String(),
			"msg", msg,
			"error", err,
		)
		return nil, errors.Wrapf(err, "failed to execute %T", msg)
	}

	data, err := json.Marshal(resp)
	if err != nil {
		ctx.Logger().Error(fmt.Sprintf("json.Marshal: failed to marshal %T response to JSON", resp),
			"from_address", msg.GetSigners()[0].String(),
			"msg", resp,
			"error", err,
		)
		return nil, errors.Wrap(err, fmt.Sprintf("marshal %T failed", resp))
	}

	ctx.Logger().Debug(fmt.Sprintf("%T execution completed", msg),
		"from_address", msg.GetSigners()[0].String(),
		"msg", msg,
	)
	return [][]byte{data}, nil
}

func (m *CustomMessenger) dispatchDexMsg(ctx sdk.Context, contractAddr sdk.AccAddress, dex bindings.Dex) ([][]byte, error) {
	switch {
	case dex.Deposit != nil:
		dex.Deposit.Creator = contractAddr.String()
		return handleDexMsg(ctx, dex.Deposit, m.DexMsgServer.Deposit)
	case dex.Withdrawal != nil:
		dex.Withdrawal.Creator = contractAddr.String()
		return handleDexMsg(ctx, dex.Withdrawal, m.DexMsgServer.Withdrawal)
	case dex.PlaceLimitOrder != nil:
		msg := dextypes.MsgPlaceLimitOrder{
			Creator:          contractAddr.String(),
			Receiver:         dex.PlaceLimitOrder.Receiver,
			TokenIn:          dex.PlaceLimitOrder.TokenIn,
			TokenOut:         dex.PlaceLimitOrder.TokenOut,
			TickIndexInToOut: dex.PlaceLimitOrder.TickIndexInToOut,
			AmountIn:         dex.PlaceLimitOrder.AmountIn,
			MaxAmountOut:     dex.PlaceLimitOrder.MaxAmountOut,
		}
		orderTypeInt, ok := dextypes.LimitOrderType_value[dex.PlaceLimitOrder.OrderType]
		if !ok {
			return nil, errors.Wrap(dextypes.ErrInvalidOrderType,
				fmt.Sprintf(
					"got \"%s\", expeted one of %s",
					dex.PlaceLimitOrder.OrderType,
					strings.Join(maps.Keys(dextypes.LimitOrderType_value), ", ")),
			)
		}
		msg.OrderType = dextypes.LimitOrderType(orderTypeInt)

		dex.PlaceLimitOrder.Creator = contractAddr.String()
		if dex.PlaceLimitOrder.ExpirationTime != nil {
			t := time.Unix(int64(*(dex.PlaceLimitOrder.ExpirationTime)), 0)
			msg.ExpirationTime = &t
		}
		return handleDexMsg(ctx, &msg, m.DexMsgServer.PlaceLimitOrder)
	case dex.CancelLimitOrder != nil:
		dex.CancelLimitOrder.Creator = contractAddr.String()
		return handleDexMsg(ctx, dex.CancelLimitOrder, m.DexMsgServer.CancelLimitOrder)
	case dex.WithdrawFilledLimitOrder != nil:
		dex.WithdrawFilledLimitOrder.Creator = contractAddr.String()
		return handleDexMsg(ctx, dex.WithdrawFilledLimitOrder, m.DexMsgServer.WithdrawFilledLimitOrder)
	case dex.MultiHopSwap != nil:
		dex.MultiHopSwap.Creator = contractAddr.String()
		return handleDexMsg(ctx, dex.MultiHopSwap, m.DexMsgServer.MultiHopSwap)
	}

	return nil, sdkerrors.ErrUnknownRequest
}

func (m *CustomMessenger) ibcTransfer(ctx sdk.Context, contractAddr sdk.AccAddress, ibcTransferMsg transferwrappertypes.MsgTransfer) ([]sdk.Event, [][]byte, error) {
	ibcTransferMsg.Sender = contractAddr.String()

	if err := ibcTransferMsg.ValidateBasic(); err != nil {
		return nil, nil, errors.Wrap(err, "failed to validate ibcTransferMsg")
	}

	response, err := m.transferKeeper.Transfer(sdk.WrapSDKContext(ctx), &ibcTransferMsg)
	if err != nil {
		ctx.Logger().Debug("transferServer.Transfer: failed to transfer",
			"from_address", contractAddr.String(),
			"msg", ibcTransferMsg,
			"error", err,
		)
		return nil, nil, errors.Wrap(err, "failed to execute IBCTransfer")
	}

	data, err := json.Marshal(response)
	if err != nil {
		ctx.Logger().Error("json.Marshal: failed to marshal MsgTransferResponse response to JSON",
			"from_address", contractAddr.String(),
			"msg", response,
			"error", err,
		)
		return nil, nil, errors.Wrap(err, "marshal json failed")
	}

	ctx.Logger().Debug("ibcTransferMsg completed",
		"from_address", contractAddr.String(),
		"msg", ibcTransferMsg,
	)
	return nil, [][]byte{data}, nil
}

func (m *CustomMessenger) updateInterchainQuery(ctx sdk.Context, contractAddr sdk.AccAddress, updateQuery *bindings.UpdateInterchainQuery) ([]sdk.Event, [][]byte, error) {
	response, err := m.performUpdateInterchainQuery(ctx, contractAddr, updateQuery)
	if err != nil {
		ctx.Logger().Debug("performUpdateInterchainQuery: failed to update interchain query",
			"from_address", contractAddr.String(),
			"msg", updateQuery,
			"error", err,
		)
		return nil, nil, errors.Wrap(err, "failed to update interchain query")
	}

	data, err := json.Marshal(response)
	if err != nil {
		ctx.Logger().Error("json.Marshal: failed to marshal UpdateInterchainQueryResponse response to JSON",
			"from_address", contractAddr.String(),
			"msg", updateQuery,
			"error", err,
		)
		return nil, nil, errors.Wrap(err, "marshal json failed")
	}

	ctx.Logger().Debug("interchain query updated",
		"from_address", contractAddr.String(),
		"msg", updateQuery,
	)
	return nil, [][]byte{data}, nil
}

func (m *CustomMessenger) performUpdateInterchainQuery(ctx sdk.Context, contractAddr sdk.AccAddress, updateQuery *bindings.UpdateInterchainQuery) (*bindings.UpdateInterchainQueryResponse, error) {
	msg := icqtypes.MsgUpdateInterchainQueryRequest{
		QueryId:               updateQuery.QueryId,
		NewKeys:               updateQuery.NewKeys,
		NewUpdatePeriod:       updateQuery.NewUpdatePeriod,
		NewTransactionsFilter: updateQuery.NewTransactionsFilter,
		Sender:                contractAddr.String(),
	}

	if err := msg.ValidateBasic(); err != nil {
		return nil, errors.Wrap(err, "failed to validate incoming UpdateInterchainQuery message")
	}

	response, err := m.Icqmsgserver.UpdateInterchainQuery(sdk.WrapSDKContext(ctx), &msg)
	if err != nil {
		return nil, errors.Wrap(err, "failed to update interchain query")
	}

	return (*bindings.UpdateInterchainQueryResponse)(response), nil
}

func (m *CustomMessenger) removeInterchainQuery(ctx sdk.Context, contractAddr sdk.AccAddress, removeQuery *bindings.RemoveInterchainQuery) ([]sdk.Event, [][]byte, error) {
	response, err := m.performRemoveInterchainQuery(ctx, contractAddr, removeQuery)
	if err != nil {
		ctx.Logger().Debug("performRemoveInterchainQuery: failed to update interchain query",
			"from_address", contractAddr.String(),
			"msg", removeQuery,
			"error", err,
		)
		return nil, nil, errors.Wrap(err, "failed to remove interchain query")
	}

	data, err := json.Marshal(response)
	if err != nil {
		ctx.Logger().Error("json.Marshal: failed to marshal RemoveInterchainQueryResponse response to JSON",
			"from_address", contractAddr.String(),
			"msg", removeQuery,
			"error", err,
		)
		return nil, nil, errors.Wrap(err, "marshal json failed")
	}

	ctx.Logger().Debug("interchain query removed",
		"from_address", contractAddr.String(),
		"msg", removeQuery,
	)
	return nil, [][]byte{data}, nil
}

func (m *CustomMessenger) performRemoveInterchainQuery(ctx sdk.Context, contractAddr sdk.AccAddress, updateQuery *bindings.RemoveInterchainQuery) (*bindings.RemoveInterchainQueryResponse, error) {
	msg := icqtypes.MsgRemoveInterchainQueryRequest{
		QueryId: updateQuery.QueryId,
		Sender:  contractAddr.String(),
	}

	if err := msg.ValidateBasic(); err != nil {
		return nil, errors.Wrap(err, "failed to validate incoming RemoveInterchainQuery message")
	}

	response, err := m.Icqmsgserver.RemoveInterchainQuery(sdk.WrapSDKContext(ctx), &msg)
	if err != nil {
		return nil, errors.Wrap(err, "failed to remove interchain query")
	}

	return (*bindings.RemoveInterchainQueryResponse)(response), nil
}

func (m *CustomMessenger) submitTx(ctx sdk.Context, contractAddr sdk.AccAddress, submitTx *bindings.SubmitTx) ([]sdk.Event, [][]byte, error) {
	response, err := m.performSubmitTx(ctx, contractAddr, submitTx)
	if err != nil {
		ctx.Logger().Debug("performSubmitTx: failed to submit interchain transaction",
			"from_address", contractAddr.String(),
			"connection_id", submitTx.ConnectionId,
			"interchain_account_id", submitTx.InterchainAccountId,
			"error", err,
		)
		return nil, nil, errors.Wrap(err, "failed to submit interchain transaction")
	}

	data, err := json.Marshal(response)
	if err != nil {
		ctx.Logger().Error("json.Marshal: failed to marshal submitTx response to JSON",
			"from_address", contractAddr.String(),
			"connection_id", submitTx.ConnectionId,
			"interchain_account_id", submitTx.InterchainAccountId,
			"error", err,
		)
		return nil, nil, errors.Wrap(err, "marshal json failed")
	}

	ctx.Logger().Debug("interchain transaction submitted",
		"from_address", contractAddr.String(),
		"connection_id", submitTx.ConnectionId,
		"interchain_account_id", submitTx.InterchainAccountId,
	)
	return nil, [][]byte{data}, nil
}

func (m *CustomMessenger) submitAdminProposal(ctx sdk.Context, contractAddr sdk.AccAddress, adminProposal *bindings.AdminProposal) ([]sdk.Event, [][]byte, error) {
	var data []byte
	err := m.validateProposalQty(adminProposal)
	if err != nil {
		return nil, nil, errors.Wrap(err, "invalid proposal quantity")
	}
	// here we handle pre-v2.0.0 style of proposals: param change, upgrade, client update
	if m.isLegacyProposal(adminProposal) {
		resp, err := m.performSubmitAdminProposalLegacy(ctx, contractAddr, adminProposal)
		if err != nil {
			ctx.Logger().Debug("performSubmitAdminProposalLegacy: failed to submitAdminProposal",
				"from_address", contractAddr.String(),
				"error", err,
			)
			return nil, nil, errors.Wrap(err, "failed to submit admin proposal legacy")
		}
		data, err = json.Marshal(resp)
		if err != nil {
			ctx.Logger().Error("json.Marshal: failed to marshal submitAdminProposalLegacy response to JSON",
				"from_address", contractAddr.String(),
				"error", err,
			)
			return nil, nil, errors.Wrap(err, "marshal json failed")
		}

		ctx.Logger().Debug("submit proposal legacy submitted",
			"from_address", contractAddr.String(),
		)
		return nil, [][]byte{data}, nil
	}

	resp, err := m.performSubmitAdminProposal(ctx, contractAddr, adminProposal)
	if err != nil {
		ctx.Logger().Debug("performSubmitAdminProposal: failed to submitAdminProposal",
			"from_address", contractAddr.String(),
			"error", err,
		)
		return nil, nil, errors.Wrap(err, "failed to submit admin proposal")
	}

	data, err = json.Marshal(resp)
	if err != nil {
		ctx.Logger().Error("json.Marshal: failed to marshal submitAdminProposal response to JSON",
			"from_address", contractAddr.String(),
			"error", err,
		)
		return nil, nil, errors.Wrap(err, "marshal json failed")
	}

	ctx.Logger().Debug("submit proposal message submitted",
		"from_address", contractAddr.String(),
	)
	return nil, [][]byte{data}, nil
}

func (m *CustomMessenger) performSubmitAdminProposalLegacy(ctx sdk.Context, contractAddr sdk.AccAddress, adminProposal *bindings.AdminProposal) (*admintypes.MsgSubmitProposalLegacyResponse, error) {
	proposal := adminProposal
	msg := admintypes.MsgSubmitProposalLegacy{Proposer: contractAddr.String()}

	switch {
	case proposal.ParamChangeProposal != nil:
		p := proposal.ParamChangeProposal
		err := msg.SetContent(&paramChange.ParameterChangeProposal{
			Title:       p.Title,
			Description: p.Description,
			Changes:     p.ParamChanges,
		})
		if err != nil {
			return nil, errors.Wrap(err, "failed to set content on ParameterChangeProposal")
		}
	case proposal.UpgradeProposal != nil:
		p := proposal.UpgradeProposal
		err := msg.SetContent(&ibcclienttypes.UpgradeProposal{
			Title:       p.Title,
			Description: p.Description,
			Plan: softwareUpgrade.Plan{
				Name:   p.Plan.Name,
				Height: p.Plan.Height,
				Info:   p.Plan.Info,
			},
			UpgradedClientState: p.UpgradedClientState,
		})
		if err != nil {
			return nil, errors.Wrap(err, "failed to set content on UpgradeProposal")
		}
	case proposal.ClientUpdateProposal != nil:
		p := proposal.ClientUpdateProposal
		err := msg.SetContent(&ibcclienttypes.ClientUpdateProposal{
			Title:              p.Title,
			Description:        p.Description,
			SubjectClientId:    p.SubjectClientId,
			SubstituteClientId: p.SubstituteClientId,
		})
		if err != nil {
			return nil, errors.Wrap(err, "failed to set content on ClientUpdateProposal")
		}
	default:
		return nil, errors.Wrapf(sdkerrors.ErrInvalidRequest, "unexpected legacy admin proposal structure: %+v", proposal)
	}

	if err := msg.ValidateBasic(); err != nil {
		return nil, errors.Wrap(err, "failed to validate incoming SubmitAdminProposal message")
	}

	response, err := m.Adminserver.SubmitProposalLegacy(sdk.WrapSDKContext(ctx), &msg)
	if err != nil {
		return nil, errors.Wrap(err, "failed to submit proposal")
	}

	ctx.Logger().Debug("submit proposal legacy processed in msg server",
		"from_address", contractAddr.String(),
	)

	return response, nil
}

func (m *CustomMessenger) performSubmitAdminProposal(ctx sdk.Context, contractAddr sdk.AccAddress, adminProposal *bindings.AdminProposal) (*admintypes.MsgSubmitProposalResponse, error) {
	proposal := adminProposal
	authority := authtypes.NewModuleAddress(admintypes.ModuleName)
	var (
		msg    *admintypes.MsgSubmitProposal
		sdkMsg sdk.Msg
	)

	cdc := m.AdminKeeper.Codec()
	err := cdc.UnmarshalInterfaceJSON([]byte(proposal.ProposalExecuteMessage.Message), &sdkMsg)
	if err != nil {
		return nil, errors.Wrap(err, "failed to unmarshall incoming sdk message")
	}

	signers := sdkMsg.GetSigners()
	if len(signers) != 1 {
		return nil, errors.Wrap(sdkerrors.ErrorInvalidSigner, "should be 1 signer")
	}
	if !signers[0].Equals(authority) {
		return nil, errors.Wrap(sdkerrors.ErrUnauthorized, "authority in incoming msg is not equal to admin module")
	}

	msg, err = admintypes.NewMsgSubmitProposal([]sdk.Msg{sdkMsg}, contractAddr)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create MsgSubmitProposal ")
	}

	if err := msg.ValidateBasic(); err != nil {
		return nil, errors.Wrap(err, "failed to validate incoming SubmitAdminProposal message")
	}

	response, err := m.Adminserver.SubmitProposal(sdk.WrapSDKContext(ctx), msg)
	if err != nil {
		return nil, errors.Wrap(err, "failed to submit proposal")
	}

	return response, nil
}

// createDenom creates a new token denom
func (m *CustomMessenger) createDenom(ctx sdk.Context, contractAddr sdk.AccAddress, createDenom *bindings.CreateDenom) ([]sdk.Event, [][]byte, error) {
	err := PerformCreateDenom(m.TokenFactory, m.Bank, ctx, contractAddr, createDenom)
	if err != nil {
		return nil, nil, errors.Wrap(err, "perform create denom")
	}
	return nil, nil, nil
}

// PerformCreateDenom is used with createDenom to create a token denom; validates the msgCreateDenom.
func PerformCreateDenom(f *tokenfactorykeeper.Keeper, _ *bankkeeper.BaseKeeper, ctx sdk.Context, contractAddr sdk.AccAddress, createDenom *bindings.CreateDenom) error {
	msgServer := tokenfactorykeeper.NewMsgServerImpl(*f)

	msgCreateDenom := tokenfactorytypes.NewMsgCreateDenom(contractAddr.String(), createDenom.Subdenom)

	if err := msgCreateDenom.ValidateBasic(); err != nil {
		return errors.Wrap(err, "failed validating MsgCreateDenom")
	}

	// Create denom
	_, err := msgServer.CreateDenom(
		sdk.WrapSDKContext(ctx),
		msgCreateDenom,
	)
	if err != nil {
		return errors.Wrap(err, "creating denom")
	}
	return nil
}

// mintTokens mints tokens of a specified denom to an address.
func (m *CustomMessenger) mintTokens(ctx sdk.Context, contractAddr sdk.AccAddress, mint *bindings.MintTokens) ([]sdk.Event, [][]byte, error) {
	err := PerformMint(m.TokenFactory, m.Bank, ctx, contractAddr, mint)
	if err != nil {
		return nil, nil, errors.Wrap(err, "perform mint")
	}
	return nil, nil, nil
}

// setBeforeSendHook sets before send hook for a specified denom.
func (m *CustomMessenger) setBeforeSendHook(ctx sdk.Context, contractAddr sdk.AccAddress, set *bindings.SetBeforeSendHook) ([]sdk.Event, [][]byte, error) {
	err := PerformSetBeforeSendHook(m.TokenFactory, ctx, contractAddr, set)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to perform set before send hook")
	}
	return nil, nil, nil
}

// PerformMint used with mintTokens to validate the mint message and mint through token factory.
func PerformMint(f *tokenfactorykeeper.Keeper, b *bankkeeper.BaseKeeper, ctx sdk.Context, contractAddr sdk.AccAddress, mint *bindings.MintTokens) error {
	rcpt, err := parseAddress(mint.MintToAddress)
	if err != nil {
		return err
	}

	coin := sdk.Coin{Denom: mint.Denom, Amount: mint.Amount}
	sdkMsg := tokenfactorytypes.NewMsgMint(contractAddr.String(), coin)
	if err = sdkMsg.ValidateBasic(); err != nil {
		return err
	}

	// Mint through token factory / message server
	msgServer := tokenfactorykeeper.NewMsgServerImpl(*f)
	_, err = msgServer.Mint(sdk.WrapSDKContext(ctx), sdkMsg)
	if err != nil {
		return errors.Wrap(err, "minting coins from message")
	}

	err = b.SendCoins(ctx, contractAddr, rcpt, sdk.NewCoins(coin))
	if err != nil {
		return errors.Wrap(err, "sending newly minted coins from message")
	}

	return nil
}

func PerformSetBeforeSendHook(f *tokenfactorykeeper.Keeper, ctx sdk.Context, contractAddr sdk.AccAddress, set *bindings.SetBeforeSendHook) error {
	sdkMsg := tokenfactorytypes.NewMsgSetBeforeSendHook(contractAddr.String(), set.Denom, set.ContractAddr)
	if err := sdkMsg.ValidateBasic(); err != nil {
		return err
	}

	// SetBeforeSendHook through token factory / message server
	msgServer := tokenfactorykeeper.NewMsgServerImpl(*f)
	_, err := msgServer.SetBeforeSendHook(sdk.WrapSDKContext(ctx), sdkMsg)
	if err != nil {
		return errors.Wrap(err, "set before send from message")
	}

	return nil
}

// changeAdmin changes the admin.
func (m *CustomMessenger) changeAdmin(ctx sdk.Context, contractAddr sdk.AccAddress, changeAdmin *bindings.ChangeAdmin) ([]sdk.Event, [][]byte, error) {
	err := ChangeAdmin(m.TokenFactory, ctx, contractAddr, changeAdmin)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to change admin")
	}

	return nil, nil, nil
}

// ChangeAdmin is used with changeAdmin to validate changeAdmin messages and to dispatch.
func ChangeAdmin(f *tokenfactorykeeper.Keeper, ctx sdk.Context, contractAddr sdk.AccAddress, changeAdmin *bindings.ChangeAdmin) error {
	newAdminAddr, err := parseAddress(changeAdmin.NewAdminAddress)
	if err != nil {
		return err
	}

	changeAdminMsg := tokenfactorytypes.NewMsgChangeAdmin(contractAddr.String(), changeAdmin.Denom, newAdminAddr.String())
	if err := changeAdminMsg.ValidateBasic(); err != nil {
		return err
	}

	msgServer := tokenfactorykeeper.NewMsgServerImpl(*f)
	_, err = msgServer.ChangeAdmin(sdk.WrapSDKContext(ctx), changeAdminMsg)
	if err != nil {
		return errors.Wrap(err, "failed changing admin from message")
	}
	return nil
}

// burnTokens burns tokens.
func (m *CustomMessenger) burnTokens(ctx sdk.Context, contractAddr sdk.AccAddress, burn *bindings.BurnTokens) ([]sdk.Event, [][]byte, error) {
	err := PerformBurn(m.TokenFactory, ctx, contractAddr, burn)
	if err != nil {
		return nil, nil, errors.Wrap(err, "perform burn")
	}

	return nil, nil, nil
}

// PerformBurn performs token burning after validating tokenBurn message.
func PerformBurn(f *tokenfactorykeeper.Keeper, ctx sdk.Context, contractAddr sdk.AccAddress, burn *bindings.BurnTokens) error {
	if burn.BurnFromAddress != "" && burn.BurnFromAddress != contractAddr.String() {
		return wasmvmtypes.InvalidRequest{Err: "BurnFromAddress must be \"\""}
	}

	coin := sdk.Coin{Denom: burn.Denom, Amount: burn.Amount}
	sdkMsg := tokenfactorytypes.NewMsgBurn(contractAddr.String(), coin)
	if err := sdkMsg.ValidateBasic(); err != nil {
		return err
	}

	// Burn through token factory / message server
	msgServer := tokenfactorykeeper.NewMsgServerImpl(*f)
	_, err := msgServer.Burn(sdk.WrapSDKContext(ctx), sdkMsg)
	if err != nil {
		return errors.Wrap(err, "burning coins from message")
	}

	return nil
}

// GetFullDenom is a function, not method, so the message_plugin can use it
func GetFullDenom(contract, subDenom string) (string, error) {
	// Address validation
	if _, err := parseAddress(contract); err != nil {
		return "", err
	}

	fullDenom, err := tokenfactorytypes.GetTokenDenom(contract, subDenom)
	if err != nil {
		return "", errors.Wrap(err, "validate sub-denom")
	}

	return fullDenom, nil
}

// parseAddress parses address from bech32 string and verifies its format.
func parseAddress(addr string) (sdk.AccAddress, error) {
	parsed, err := sdk.AccAddressFromBech32(addr)
	if err != nil {
		return nil, errors.Wrap(err, "address from bech32")
	}

	err = sdk.VerifyAddressFormat(parsed)
	if err != nil {
		return nil, errors.Wrap(err, "verify address format")
	}

	return parsed, nil
}

func (m *CustomMessenger) performSubmitTx(ctx sdk.Context, contractAddr sdk.AccAddress, submitTx *bindings.SubmitTx) (*bindings.SubmitTxResponse, error) {
	tx := ictxtypes.MsgSubmitTx{
		FromAddress:         contractAddr.String(),
		ConnectionId:        submitTx.ConnectionId,
		Memo:                submitTx.Memo,
		InterchainAccountId: submitTx.InterchainAccountId,
		Timeout:             submitTx.Timeout,
		Fee:                 submitTx.Fee,
	}
	for _, msg := range submitTx.Msgs {
		tx.Msgs = append(tx.Msgs, &types.Any{
			TypeUrl: msg.TypeURL,
			Value:   msg.Value,
		})
	}

	if err := tx.ValidateBasic(); err != nil {
		return nil, errors.Wrap(err, "failed to validate incoming SubmitTx message")
	}

	response, err := m.Ictxmsgserver.SubmitTx(sdk.WrapSDKContext(ctx), &tx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to submit interchain transaction")
	}

	return (*bindings.SubmitTxResponse)(response), nil
}

func (m *CustomMessenger) registerInterchainAccount(ctx sdk.Context, contractAddr sdk.AccAddress, reg *bindings.RegisterInterchainAccount) ([]sdk.Event, [][]byte, error) {
	response, err := m.performRegisterInterchainAccount(ctx, contractAddr, reg)
	if err != nil {
		ctx.Logger().Debug("performRegisterInterchainAccount: failed to register interchain account",
			"from_address", contractAddr.String(),
			"connection_id", reg.ConnectionId,
			"interchain_account_id", reg.InterchainAccountId,
			"error", err,
		)
		return nil, nil, errors.Wrap(err, "failed to register interchain account")
	}

	data, err := json.Marshal(response)
	if err != nil {
		ctx.Logger().Error("json.Marshal: failed to marshal register interchain account response to JSON",
			"from_address", contractAddr.String(),
			"connection_id", reg.ConnectionId,
			"interchain_account_id", reg.InterchainAccountId,
			"error", err,
		)
		return nil, nil, errors.Wrap(err, "marshal json failed")
	}

	ctx.Logger().Debug("registered interchain account",
		"from_address", contractAddr.String(),
		"connection_id", reg.ConnectionId,
		"interchain_account_id", reg.InterchainAccountId,
	)
	return nil, [][]byte{data}, nil
}

func (m *CustomMessenger) performRegisterInterchainAccount(ctx sdk.Context, contractAddr sdk.AccAddress, reg *bindings.RegisterInterchainAccount) (*bindings.RegisterInterchainAccountResponse, error) {
	msg := ictxtypes.MsgRegisterInterchainAccount{
		FromAddress:         contractAddr.String(),
		ConnectionId:        reg.ConnectionId,
		InterchainAccountId: reg.InterchainAccountId,
		RegisterFee:         getRegisterFee(reg.RegisterFee),
	}
	if err := msg.ValidateBasic(); err != nil {
		return nil, errors.Wrap(err, "failed to validate incoming RegisterInterchainAccount message")
	}

	response, err := m.Ictxmsgserver.RegisterInterchainAccount(sdk.WrapSDKContext(ctx), &msg)
	if err != nil {
		return nil, errors.Wrap(err, "failed to register interchain account")
	}

	return (*bindings.RegisterInterchainAccountResponse)(response), nil
}

func (m *CustomMessenger) registerInterchainQuery(ctx sdk.Context, contractAddr sdk.AccAddress, reg *bindings.RegisterInterchainQuery) ([]sdk.Event, [][]byte, error) {
	response, err := m.performRegisterInterchainQuery(ctx, contractAddr, reg)
	if err != nil {
		ctx.Logger().Debug("performRegisterInterchainQuery: failed to register interchain query",
			"from_address", contractAddr.String(),
			"query_type", reg.QueryType,
			"kv_keys", icqtypes.KVKeys(reg.Keys).String(),
			"transactions_filter", reg.TransactionsFilter,
			"connection_id", reg.ConnectionId,
			"update_period", reg.UpdatePeriod,
			"error", err,
		)
		return nil, nil, errors.Wrap(err, "failed to register interchain query")
	}

	data, err := json.Marshal(response)
	if err != nil {
		ctx.Logger().Error("json.Marshal: failed to marshal register interchain query response to JSON",
			"from_address", contractAddr.String(),
			"kv_keys", icqtypes.KVKeys(reg.Keys).String(),
			"transactions_filter", reg.TransactionsFilter,
			"connection_id", reg.ConnectionId,
			"update_period", reg.UpdatePeriod,
			"error", err,
		)
		return nil, nil, errors.Wrap(err, "marshal json failed")
	}

	ctx.Logger().Debug("registered interchain query",
		"from_address", contractAddr.String(),
		"query_type", reg.QueryType,
		"kv_keys", icqtypes.KVKeys(reg.Keys).String(),
		"transactions_filter", reg.TransactionsFilter,
		"connection_id", reg.ConnectionId,
		"update_period", reg.UpdatePeriod,
		"query_id", response.Id,
	)
	return nil, [][]byte{data}, nil
}

func (m *CustomMessenger) performRegisterInterchainQuery(ctx sdk.Context, contractAddr sdk.AccAddress, reg *bindings.RegisterInterchainQuery) (*bindings.RegisterInterchainQueryResponse, error) {
	msg := icqtypes.MsgRegisterInterchainQuery{
		Keys:               reg.Keys,
		TransactionsFilter: reg.TransactionsFilter,
		QueryType:          reg.QueryType,
		ConnectionId:       reg.ConnectionId,
		UpdatePeriod:       reg.UpdatePeriod,
		Sender:             contractAddr.String(),
	}
	if err := msg.ValidateBasic(); err != nil {
		return nil, errors.Wrap(err, "failed to validate incoming RegisterInterchainQuery message")
	}

	response, err := m.Icqmsgserver.RegisterInterchainQuery(sdk.WrapSDKContext(ctx), &msg)
	if err != nil {
		return nil, errors.Wrap(err, "failed to register interchain query")
	}

	return (*bindings.RegisterInterchainQueryResponse)(response), nil
}

func (m *CustomMessenger) validateProposalQty(proposal *bindings.AdminProposal) error {
	qty := 0
	if proposal.ParamChangeProposal != nil {
		qty++
	}
	if proposal.ClientUpdateProposal != nil {
		qty++
	}
	if proposal.UpgradeProposal != nil {
		qty++
	}
	if proposal.ProposalExecuteMessage != nil {
		qty++
	}

	switch qty {
	case 1:
		return nil
	case 0:
		return fmt.Errorf("no admin proposal type is present in message")
	default:
		return fmt.Errorf("more than one admin proposal type is present in message")
	}
}

func (m *CustomMessenger) isLegacyProposal(proposal *bindings.AdminProposal) bool {
	switch {
	case proposal.ParamChangeProposal != nil,
		proposal.UpgradeProposal != nil,
		proposal.ClientUpdateProposal != nil:
		return true
	default:
		return false
	}
}

func (m *CustomMessenger) addSchedule(ctx sdk.Context, contractAddr sdk.AccAddress, addSchedule *bindings.AddSchedule) ([]sdk.Event, [][]byte, error) {
	if !m.isAdmin(ctx, contractAddr) {
		return nil, nil, errors.Wrap(sdkerrors.ErrUnauthorized, "only admin can add schedule")
	}

	msgs := make([]crontypes.MsgExecuteContract, 0, len(addSchedule.Msgs))
	for _, msg := range addSchedule.Msgs {
		msgs = append(msgs, crontypes.MsgExecuteContract{
			Contract: msg.Contract,
			Msg:      msg.Msg,
		})
	}

	err := m.CronKeeper.AddSchedule(ctx, addSchedule.Name, addSchedule.Period, msgs)
	if err != nil {
		ctx.Logger().Error("failed to addSchedule",
			"from_address", contractAddr.String(),
			"error", err,
		)
		return nil, nil, errors.Wrap(err, "marshal json failed")
	}

	resp := bindings.AddScheduleResponse{}
	data, err := json.Marshal(&resp)
	if err != nil {
		ctx.Logger().Error("json.Marshal: failed to marshal add schedule response to JSON",
			"from_address", contractAddr.String(),
			"error", err,
		)
		return nil, nil, errors.Wrap(err, "marshal json failed")
	}

	ctx.Logger().Debug("schedule added",
		"from_address", contractAddr.String(),
		"name", addSchedule.Name,
		"period", addSchedule.Period,
	)
	return nil, [][]byte{data}, nil
}

func (m *CustomMessenger) removeSchedule(ctx sdk.Context, contractAddr sdk.AccAddress, removeSchedule *bindings.RemoveSchedule) ([]sdk.Event, [][]byte, error) {
	params := m.CronKeeper.GetParams(ctx)
	if !m.isAdmin(ctx, contractAddr) && contractAddr.String() != params.SecurityAddress {
		return nil, nil, errors.Wrap(sdkerrors.ErrUnauthorized, "only admin or security dao can remove schedule")
	}

	m.CronKeeper.RemoveSchedule(ctx, removeSchedule.Name)

	resp := bindings.RemoveScheduleResponse{}
	data, err := json.Marshal(&resp)
	if err != nil {
		ctx.Logger().Error("json.Marshal: failed to marshal remove schedule response to JSON",
			"from_address", contractAddr.String(),
			"error", err,
		)
		return nil, nil, errors.Wrap(err, "marshal json failed")
	}

	ctx.Logger().Debug("schedule removed",
		"from_address", contractAddr.String(),
		"name", removeSchedule.Name,
	)
	return nil, [][]byte{data}, nil
}

func (m *CustomMessenger) resubmitFailure(ctx sdk.Context, contractAddr sdk.AccAddress, resubmitFailure *bindings.ResubmitFailure) ([]sdk.Event, [][]byte, error) {
	failure, err := m.ContractmanagerKeeper.GetFailure(ctx, contractAddr, resubmitFailure.FailureId)
	if err != nil {
		return nil, nil, errors.Wrap(sdkerrors.ErrNotFound, "no failure found to resubmit")
	}

	err = m.ContractmanagerKeeper.ResubmitFailure(ctx, contractAddr, failure)
	if err != nil {
		ctx.Logger().Error("failed to resubmitFailure",
			"from_address", contractAddr.String(),
			"error", err,
		)
		return nil, nil, errors.Wrap(err, "failed to resubmitFailure")
	}

	resp := bindings.ResubmitFailureResponse{FailureId: failure.Id}
	data, err := json.Marshal(&resp)
	if err != nil {
		ctx.Logger().Error("json.Marshal: failed to marshal remove resubmitFailure response to JSON",
			"from_address", contractAddr.String(),
			"error", err,
		)
		return nil, nil, errors.Wrap(err, "marshal json failed")
	}

	return nil, [][]byte{data}, nil
}

func (m *CustomMessenger) isAdmin(ctx sdk.Context, contractAddr sdk.AccAddress) bool {
	for _, admin := range m.AdminKeeper.GetAdmins(ctx) {
		if admin == contractAddr.String() {
			return true
		}
	}

	return false
}

func getRegisterFee(fee sdk.Coins) sdk.Coins {
	if fee == nil {
		return make(sdk.Coins, 0)
	}
	return fee
}
