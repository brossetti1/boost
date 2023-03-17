package types

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"

	"github.com/filecoin-project/boost/transport/httptransport/util"
	"github.com/filecoin-project/boost/transport/types"
	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/go-fil-markets/storagemarket"
	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/go-state-types/builtin/v9/market"
	"github.com/filecoin-project/go-state-types/crypto"
	"github.com/filecoin-project/lotus/api"
	"github.com/filecoin-project/specs-storage/storage"
	"github.com/google/uuid"
	"github.com/ipfs/go-cid"
	"github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
)

//go:generate cbor-gen-for --map-encoding StorageAsk DealParamsV120 DealParams Transfer DealResponse DealStatusRequest DealStatusResponse DealStatus
//go:generate go run github.com/golang/mock/mockgen -destination=mock_types/mocks.go -package=mock_types . PieceAdder,CommpCalculator,DealPublisher,ChainDealManager,IndexProvider

// StorageAsk defines the parameters by which a miner will choose to accept or
// reject a deal. Note: making a storage deal proposal which matches the miner's
// ask is a precondition, but not sufficient to ensure the deal is accepted (the
// storage provider may run its own decision logic).
type StorageAsk struct {
	// Price per GiB / Epoch
	Price         abi.TokenAmount
	VerifiedPrice abi.TokenAmount

	MinPieceSize abi.PaddedPieceSize
	MaxPieceSize abi.PaddedPieceSize
	Miner        address.Address
}

// DealStatusRequest is sent to get the current state of a deal from a
// storage provider
type DealStatusRequest struct {
	DealUUID  uuid.UUID
	Signature crypto.Signature
}

// DealStatusResponse is the current state of a deal
type DealStatusResponse struct {
	DealUUID uuid.UUID
	// Error is non-empty if there is an error getting the deal status
	// (eg invalid request signature)
	Error          string
	DealStatus     *DealStatus
	IsOffline      bool
	TransferSize   uint64
	NBytesReceived uint64
}

type DealStatus struct {
	// Error is non-empty if the deal is in the error state
	Error string
	// Status is a string corresponding to a deal checkpoint
	Status string
	// SealingStatus is the sealing status reported by lotus miner
	SealingStatus string
	// Proposal is the deal proposal
	Proposal market.DealProposal
	// SignedProposalCid is the cid of the client deal proposal + signature
	SignedProposalCid cid.Cid
	// PublishCid is the cid of the Publish message sent on chain, if the deal
	// has reached the publish stage
	PublishCid *cid.Cid
	// ChainDealID is the id of the deal in chain state
	ChainDealID abi.DealID
}

type DealParams struct {
	DealUUID           uuid.UUID
	IsOffline          bool
	ClientDealProposal market.ClientDealProposal
	DealDataRoot       cid.Cid
	Transfer           Transfer // Transfer params will be the zero value if this is an offline deal
	RemoveUnsealedCopy bool
	SkipIPNIAnnounce   bool
}

// Transfer has the parameters for a data transfer
type Transfer struct {
	// The type of transfer eg "http"
	Type string
	// An optional ID that can be supplied by the client to identify the deal
	ClientID string
	// A byte array containing marshalled data specific to the transfer type
	// eg a JSON encoded struct { URL: "<url>", Headers: {...} }
	Params []byte
	// The size of the data transferred in bytes
	Size uint64
}

// ToURL takes a multiaddr of the form:
// /dns/thing.com/http/urlescape<path/to/root>
// /ip/192.168.0.1/tcp/80/http
func ToURL(ma multiaddr.Multiaddr) (*url.URL, error) {
	// host should be either the dns name or the IP
	_, host, err := manet.DialArgs(ma)
	if err != nil {
		return nil, err
	}
	if ip := net.ParseIP(host); ip != nil {
		if !ip.To4().Equal(ip) {
			// raw v6 IPs need `[ip]` encapsulation.
			host = fmt.Sprintf("[%s]", host)
		}
	}

	protos := ma.Protocols()
	pm := make(map[int]string, len(protos))
	for _, p := range protos {
		v, err := ma.ValueForProtocol(p.Code)
		if err == nil {
			pm[p.Code] = v
		}
	}

	scheme := "http"
	if _, ok := pm[multiaddr.P_HTTPS]; ok {
		scheme = "https"
	} else if _, ok = pm[multiaddr.P_HTTP]; ok {
		// /tls/http == /https
		if _, ok = pm[multiaddr.P_TLS]; ok {
			scheme = "https"
		}
	} else if _, ok = pm[multiaddr.P_WSS]; ok {
		scheme = "wss"
	} else if _, ok = pm[multiaddr.P_WS]; ok {
		scheme = "ws"
		// /tls/ws == /wss
		if _, ok = pm[multiaddr.P_TLS]; ok {
			scheme = "wss"
		}
	}

	path := ""
	if pb, ok := pm[0x300200]; ok {
		path, err = url.PathUnescape(pb)
		if err != nil {
			path = ""
		}
	}

	out := url.URL{
		Scheme: scheme,
		Host:   host,
		Path:   path,
	}
	return &out, nil
}

func (t *Transfer) Host() (string, error) {
	if t.Type != "http" && t.Type != "libp2p" {
		return "", fmt.Errorf("cannot parse params for unrecognized transfer type '%s'", t.Type)
	}

	// de-serialize transport opaque token
	tInfo := &types.HttpRequest{}
	if err := json.Unmarshal(t.Params, tInfo); err != nil {
		return "", fmt.Errorf("failed to de-serialize transport params bytes '%s': %w", string(t.Params), err)
	}

	// Parse http / multiaddr url
	u, err := util.ParseUrl(tInfo.URL)
	if err != nil {
		return "", fmt.Errorf("cannot parse url '%s': %w", tInfo.URL, err)
	}

	// If the url is in libp2p format
	if u.Scheme == util.Libp2pScheme {
		// Get the host from the multiaddr
		mahttp, err := ToURL(u.Multiaddr)
		if err != nil {
			return "", err
		}
		return mahttp.Host, nil
	}

	// Otherwise parse as an http url
	httpUrl, err := url.Parse(u.Url)
	if err != nil {
		return "", fmt.Errorf("cannot parse url '%s' from '%s': %w", u.Url, tInfo.URL, err)
	}

	return httpUrl.Host, nil
}

type DealResponse struct {
	Accepted bool
	// Message is the reason the deal proposal was rejected. It is empty if
	// the deal was accepted.
	Message string
}

type PieceAdder interface {
	AddPiece(ctx context.Context, size abi.UnpaddedPieceSize, r io.Reader, d api.PieceDealInfo) (abi.SectorNumber, abi.PaddedPieceSize, error)
}

type CommpCalculator interface {
	ComputeDataCid(ctx context.Context, pieceSize abi.UnpaddedPieceSize, pieceData storage.Data) (abi.PieceInfo, error)
}

type DealPublisher interface {
	Publish(ctx context.Context, deal market.ClientDealProposal) (cid.Cid, error)
}

type ChainDealManager interface {
	WaitForPublishDeals(ctx context.Context, publishCid cid.Cid, proposal market.DealProposal) (*storagemarket.PublishDealsWaitResult, error)
}

type IndexProvider interface {
	Enabled() bool
	AnnounceBoostDeal(ctx context.Context, pds *ProviderDealState) (cid.Cid, error)
	Start(ctx context.Context)
}

type AskGetter interface {
	GetAsk() *storagemarket.SignedStorageAsk
}

type SignatureVerifier interface {
	VerifySignature(ctx context.Context, sig crypto.Signature, addr address.Address, input []byte) (bool, error)
}
