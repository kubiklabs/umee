package cli

import (
	"fmt"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/client/tx"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	ethcmn "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/spf13/cobra"

	"github.com/umee-network/umee/x/peggy/types"
)

// Flag constants
const (
	FlagOrchEthSig = "orch-eth-sig"
	FlagEthPrivKey = "eth-priv-key"
)

func GetTxCmd(storeKey string) *cobra.Command {
	peggyTxCmd := &cobra.Command{
		Use:                        types.ModuleName,
		Short:                      fmt.Sprintf("Transaction commands for the %s module", types.ModuleName),
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}

	peggyTxCmd.AddCommand([]*cobra.Command{
		CmdSendToEth(),
		CmdRequestBatch(),
		CmdSetOrchestratorAddress(),
	}...)

	return peggyTxCmd
}

func CmdSendToEth() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "send-to-eth [eth-dest-addr] [amount] [bridge-fee]",
		Short: "Create a new un-batched tx in the tx pool to withdraw funds from Cosmos to Ethereum",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			cliCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}

			amount, err := sdk.ParseCoinNormalized(args[1])
			if err != nil {
				return sdkerrors.Wrap(err, "invalid amount")
			}

			bridgeFee, err := sdk.ParseCoinNormalized(args[2])
			if err != nil {
				return sdkerrors.Wrap(err, "invalid bridge fee")
			}

			msg := types.NewMsgSendToEth(cliCtx.GetFromAddress(), args[0], amount, bridgeFee)
			if err := msg.ValidateBasic(); err != nil {
				return err
			}

			return tx.GenerateOrBroadcastTxCLI(cliCtx, cmd.Flags(), msg)
		},
	}

	flags.AddTxFlagsToCmd(cmd)
	return cmd
}

func CmdRequestBatch() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "build-batch [denom]",
		Short: "Build a new tx batch for pooled Cosmos withdrawal transactions",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cliCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}

			msg := types.NewMsgRequestBatch(cliCtx.GetFromAddress(), args[0])
			if err := msg.ValidateBasic(); err != nil {
				return err
			}

			return tx.GenerateOrBroadcastTxCLI(cliCtx, cmd.Flags(), msg)
		},
	}

	flags.AddTxFlagsToCmd(cmd)
	return cmd
}

func CmdSetOrchestratorAddress() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set-orchestrator-address [validator-acc-addr] [orchestrator-addr] [ethereum-addr]",
		Short: "Allows validators to delegate their voting responsibilities to a given key.",
		Long: `Set a validator's Ethereum and orchestrator addresses. The delegate
key owner must sign over a binary Proto-encoded SetOrchestratorAddressesSignMsg
message. The message contains the delegated key owner's address and current
account nonce.

An operator may provide an already generated signature via the --orch-eth-sig flag
or have the Ethereum signature automatically generated by providing the Ethereum
private key via the --eth-priv-key flag. If generating the Ethereum signature
manually, the operator must use the current account nonce.`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cmd.Flags().Set(flags.FlagFrom, args[0]); err != nil {
				return err
			}

			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}

			orcAddr, err := sdk.AccAddressFromBech32(args[1])
			if err != nil {
				return err
			}

			valAccAddr := clientCtx.GetFromAddress()

			var ethSig []byte
			if s, err := cmd.Flags().GetString(FlagOrchEthSig); len(s) > 0 && err == nil {
				ethSig, err = hexutil.Decode(s)
				if err != nil {
					return err
				}
			} else {
				ethPrivKeyStr, err := cmd.Flags().GetString(FlagEthPrivKey)
				if err != nil {
					return err
				}

				privKeyBz, err := hexutil.Decode(ethPrivKeyStr)
				if err != nil {
					return fmt.Errorf("failed to parse Ethereum private key: %w", err)
				}

				ethPrivKey, err := ethcrypto.ToECDSA(privKeyBz)
				if err != nil {
					return fmt.Errorf("failed to convert private key: %w", err)
				}

				queryClient := authtypes.NewQueryClient(clientCtx)
				res, err := queryClient.Account(cmd.Context(), &authtypes.QueryAccountRequest{Address: valAccAddr.String()})
				if err != nil {
					return fmt.Errorf("failed to query account: %w", err)
				}

				var acc authtypes.AccountI
				if err := clientCtx.Codec.UnpackAny(res.Account, &acc); err != nil {
					return fmt.Errorf("failed to unmarshal account: %w", err)
				}

				signMsgBz := clientCtx.Codec.MustMarshal(&types.SetOrchestratorAddressesSignMsg{
					ValidatorAddress: sdk.ValAddress(valAccAddr).String(),
					Nonce:            acc.GetSequence(),
				})

				ethSig, err = types.NewEthereumSignature(ethcrypto.Keccak256Hash(signMsgBz), ethPrivKey)
				if err != nil {
					return fmt.Errorf("failed to create Ethereum signature: %w", err)
				}
			}

			msg := types.NewMsgSetOrchestratorAddress(valAccAddr, orcAddr, ethcmn.HexToAddress(args[2]), ethSig)
			if err := msg.ValidateBasic(); err != nil {
				return err
			}

			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}

	cmd.Flags().String(FlagOrchEthSig, "", "The Ethereum signature used to set the Orchestrator addresses")
	cmd.Flags().String(FlagEthPrivKey, "", "The Ethereum private key used to set the Orchestrator addresses")

	flags.AddTxFlagsToCmd(cmd)
	return cmd
}