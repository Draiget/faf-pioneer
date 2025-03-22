package webrtc

import (
	"context"
	"faf-pioneer/applog"
	"faf-pioneer/icebreaker"
	"faf-pioneer/launcher"
	"faf-pioneer/util"
	"github.com/pion/webrtc/v4"
	"go.uber.org/zap"
	"net"
	"sync"
	"time"
	"unsafe"
)

type onPeerCandidatesGatheredCallback = func(*webrtc.SessionDescription, []webrtc.ICECandidate)

const (
	maxLobbyPeers = 30
)
const (
	// peerDisconnectedTimeout is a duration without network activity before an Agent is considered disconnected.
	// Default is 5 Seconds.
	peerDisconnectedTimeout = time.Second * 10
	// peerFailedTimeout is a duration without network activity before an Agent is considered
	// failed after disconnected.
	// Default is 25 Seconds.
	peerFailedTimeout = time.Second * 30
	// peerKeepAliveInterval is an interval how often the ICE Agent sends extra traffic if there is no activity,
	// if media is flowing no traffic will be sent.
	peerKeepAliveInterval = time.Second * 5
	// peerReconnectionInterval is an interval how often we will be trying to reconnect after failed reconnection
	// attempt when Peer goes to Failed/Closed state.
	peerReconnectionInterval = time.Second * 10
)

type PeerHandler interface {
	AddPeerIfMissing(playerId uint) PeerMeta
	GetPeerById(playerId uint) *Peer
}

type PeerManager struct {
	ctx                  context.Context
	localUserId          uint
	gameId               uint64
	peerMu               sync.Mutex
	peers                map[uint]*Peer
	peerAddressesV4      map[uint32]bool
	peerAddressesV6      map[[4]uint32]bool
	peerAddressesMutex   sync.RWMutex
	icebreakerClient     *icebreaker.Client
	icebreakerEvents     <-chan icebreaker.EventMessage
	turnServer           []webrtc.ICEServer
	gameUdpPort          uint
	forceTurnRelay       bool
	reconnectionRequests chan uint
}

func NewPeerManager(
	ctx context.Context,
	icebreakerClient *icebreaker.Client,
	launcherInfo *launcher.Info,
	gameUdpPort uint,
	turnServer []webrtc.ICEServer,
	icebreakerEvents <-chan icebreaker.EventMessage,
) *PeerManager {
	peerManager := PeerManager{
		ctx:                  ctx,
		localUserId:          launcherInfo.UserId,
		gameId:               launcherInfo.GameId,
		peers:                make(map[uint]*Peer),
		icebreakerClient:     icebreakerClient,
		icebreakerEvents:     icebreakerEvents,
		turnServer:           turnServer,
		gameUdpPort:          gameUdpPort,
		forceTurnRelay:       launcherInfo.ForceTurnRelay,
		reconnectionRequests: make(chan uint, maxLobbyPeers),
		peerAddressesV4:      make(map[uint32]bool, maxLobbyPeers),
		peerAddressesV6:      make(map[[4]uint32]bool, maxLobbyPeers),
	}

	// Note:
	// Setting maxLobbyPeers for `peerAddressesV4` and `peerAddressesV6` initial capacity does not bound its size:
	// maps grow to accommodate the number of items stored in them.

	return &peerManager
}

func (p *PeerManager) Start() {
	go p.runReconnectionManagement()

	for {
		select {
		case msg, ok := <-p.icebreakerEvents:
			if !ok {
				return
			}

			p.handleIceBreakerEvent(msg)
		case <-p.ctx.Done():
			return
		}
	}
}

func (p *PeerManager) runReconnectionManagement() {
	for {
		select {
		case peerId := <-p.reconnectionRequests:
			p.handleReconnection(peerId)
		case <-p.ctx.Done():
			return
		}
	}
}

func (p *PeerManager) handleReconnection(playerId uint) {
	applog.Debug("Handling reconnection for peer", zap.Uint("playerId", playerId))
	peer, exists := p.peers[playerId]
	if !exists || peer.IsActive() || peer.IsDisabled() {
		return
	}

	if err := peer.ConnectWithRetry(p.turnServer, peerReconnectionInterval); err != nil {
		applog.Error("Reconnection failed for peer", zap.Uint("playerId", playerId), zap.Error(err))
		p.scheduleReconnection(playerId)
		return
	}

	peer.reconnectionScheduled = false
	applog.Info("Reconnection succeeded for peer", zap.Uint("playerId", playerId))
}

func (p *PeerManager) scheduleReconnection(playerId uint) {
	peer := p.GetPeerById(playerId)
	if peer == nil {
		return
	}

	peer.reconnectMu.Lock()
	defer peer.reconnectMu.Unlock()
	if peer.reconnectionScheduled {
		applog.Info("Reconnection already scheduled for peer", zap.Uint("playerId", playerId))
		return
	}
	peer.reconnectionScheduled = true

	select {
	case p.reconnectionRequests <- playerId:
		applog.Info("Scheduled reconnection for peer", zap.Uint("playerId", playerId))
	default:
		applog.Info("Reconnection already scheduled (channel full) for peer", zap.Uint("playerId", playerId))
	}
}

func (p *PeerManager) handleIceBreakerEvent(msg icebreaker.EventMessage) {
	switch event := msg.(type) {
	case *icebreaker.ConnectedMessage:
		applog.Info("Connecting to peer", zap.Any("event", event))
		p.addPeerIfMissing(event.SenderID)
	case *icebreaker.CandidatesMessage:
		applog.Info("Received CandidatesMessage", zap.Any("event", event))
		peer := p.peers[event.SenderID]

		if peer == nil {
			peer = p.addPeerIfMissing(event.SenderID)
			if peer == nil {
				applog.Error("Peer still nil after adding it as missing one")
				return
			}
		}

		if peer.connection.ICEConnectionState() != webrtc.ICEConnectionStateConnected {
			err := peer.AddCandidates(event.Session, event.Candidates)
			if err != nil {
				panic(err)
			}
		}
	default:
		applog.Info("Received unknown event type", zap.Any("event", event))
	}
}

func (p *PeerManager) StoreAllowedAddr(addr *net.IPAddr) {
	if len(addr.IP) == net.IPv4len {
		p.peerAddressesMutex.Lock()
		defer p.peerAddressesMutex.Unlock()

		numericIp := *(*uint32)(unsafe.Pointer(&addr.IP[0]))
		p.peerAddressesV4[numericIp] = true
		return
	}

	if len(addr.IP) == net.IPv6len {
		p.peerAddressesMutex.Lock()
		defer p.peerAddressesMutex.Unlock()

		numericIp := *(*[4]uint32)(unsafe.Pointer(&addr.IP[0]))
		p.peerAddressesV6[numericIp] = true
		return
	}
}

func (p *PeerManager) RemoveAllowedAddr(addr *net.IPAddr) {
	if len(addr.IP) == net.IPv4len {
		p.peerAddressesMutex.Lock()
		defer p.peerAddressesMutex.Unlock()

		numericIp := *(*uint32)(unsafe.Pointer(&addr.IP[0]))
		delete(p.peerAddressesV4, numericIp)
		return
	}

	if len(addr.IP) == net.IPv6len {
		p.peerAddressesMutex.Lock()
		defer p.peerAddressesMutex.Unlock()

		numericIp := *(*[4]uint32)(unsafe.Pointer(&addr.IP[0]))
		delete(p.peerAddressesV6, numericIp)
		return
	}
}

func (p *PeerManager) AddPeerIfMissing(playerId uint) PeerMeta {
	return p.addPeerIfMissing(playerId)
}

func (p *PeerManager) GetPeerById(playerId uint) *Peer {
	existingPeer, exists := p.peers[playerId]
	if exists {
		return existingPeer
	}

	return nil
}

func (p *PeerManager) GetAllPeerIds() []uint {
	ids := make([]uint, 0, len(p.peers))
	for id := range p.peers {
		ids = append(ids, id)
	}
	return ids
}

func (p *PeerManager) addPeerIfMissing(playerId uint) *Peer {
	if peer, exists := p.peers[playerId]; exists {
		if peer.IsActive() {
			applog.Info("Peer already exists and is active", zap.Uint("playerId", playerId))
			return peer
		}

		applog.Info("Peer exists but is inactive, scheduling reconnection", zap.Uint("playerId", playerId))
		p.scheduleReconnection(playerId)
		return peer
	}

	applog.Info("Creating new peer", zap.Uint("playerId", playerId))

	// The smaller user id is always the offerer.
	isOfferer := p.localUserId < playerId
	peerUdpPort, err := util.GetFreeUdpPort()
	if err != nil {
		applog.Error("Failed to get UDP port for new peer", zap.Uint("playerId", playerId), zap.Error(err))
		return nil
	}

	newPeer, err := CreatePeer(
		p.ctx,
		isOfferer,
		playerId,
		p,
		peerUdpPort,
		p.gameUdpPort,
	)
	if err != nil {
		applog.Error("Failed to create peer", zap.Uint("playerId", playerId), zap.Error(err))
		return nil
	}

	newPeer.onStateChanged = p.onPeerStateChanged

	p.peers[playerId] = newPeer
	return newPeer
}

func (p *PeerManager) onPeerStateChanged(peer *Peer, state webrtc.PeerConnectionState) {
	applog.FromContext(peer.context).Info(
		"Peer connection state has changed",
		zap.String("state", state.String()),
	)

	// Notify peer connection state change for `waitForCandidatePair`.
	select {
	case peer.connectionStateCh <- state:
	default:
	}

	switch state {
	case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed:
		select {
		case <-peer.localAddrReady:
		default:
			close(peer.localAddrReady)
		}

		p.onPeerDisconnected(peer)

		close(peer.candidatePairCh)
		close(peer.connectionStateCh)

		applog.FromContext(peer.context).Info(
			"Peer connection failed or closed, scheduling immediate reconnection")

		if state == webrtc.PeerConnectionStateFailed && peer.forceTurnRelay {
			applog.FromContext(peer.context).Info("Switching to fallback relay All policy")
			peer.forceTurnRelay = false
		}
		p.scheduleReconnection(peer.PeerId())
		break
	case webrtc.PeerConnectionStateDisconnected:
		// WebRTC documentation saying:
		// The ICE Agent has determined that connectivity is currently lost for this RTCIceTransport.
		// This is a transient state that may trigger intermittently (and resolve itself without action)
		// on a flaky network.
		// However:
		// The way this state is determined is implementation dependent.
		// Suggesting to handle reconnection only on Failed or Closed state instead.
		applog.FromContext(peer.context).Info("Peer disconnected, waiting to see if it recovers")
		break

	case webrtc.PeerConnectionStateConnected:
		peer.reconnectionScheduled = false
		// Gather initial states.
		peer.updateCandidateMap()

		// Theoretically there could be a situation when `webrtc.PeerConnection` does not gather
		// statistics yet when we entered `Connected` state as a webrtc.ICECandidatePairStats might be in a
		// webrtc.StatsICECandidatePairStateInProgress state here.
		// For that we will call another goroutine that will be waiting for waitForCandidatePair to
		// set the local address and allow register data channel in peer.

		go func() {
			pair, ok := waitForCandidatePair(peer.candidatePairCh, peer.connectionStateCh, peer.disabledCh)
			if !ok {
				applog.FromContext(peer.context).Error(
					"Failed to receive succeeded candidate pair, connection lost or peer is in disabled state")
				return
			}

			applog.Debug("Candidate pairs received, updating map")

			// Make sure our map will now have fully updated candidate stats after `waitForCandidatePair`.
			peer.updateCandidateMap()

			localCandidate, okLocal := peer.candidateMap[pair.LocalCandidateID]
			remoteCandidate, okRemote := peer.candidateMap[pair.RemoteCandidateID]
			if !okLocal || !okRemote {
				applog.FromContext(peer.context).Error("Could not find candidate pair in peer stats")
				return
			}

			// Ignoring resolve error as it shouldn't really happen as WebRTC will be putting
			// a valid IP address here.
			localAddress, _ := net.ResolveIPAddr("ip", localCandidate.IP)
			remoteAddress, _ := net.ResolveIPAddr("ip", remoteCandidate.IP)

			peer.localAddress = localAddress
			peer.remoteAddress = remoteAddress
			close(peer.localAddrReady)

			p.onPeerConnected(peer, remoteAddress)

			applog.FromContext(peer.context).Info(
				"Local candidate",
				zap.Any("candidate", localCandidate),
			)
			applog.FromContext(peer.context).Info(
				"Remote candidate",
				zap.Any("candidate", remoteCandidate),
			)
		}()
		break
	default:
		break
	}
}

func waitForCandidatePair(
	candidatePairCh <-chan webrtc.ICECandidatePairStats,
	connectionStateCh <-chan webrtc.PeerConnectionState,
	disabledCh <-chan struct{},
) (webrtc.ICECandidatePairStats, bool) {
	for {
		select {
		case pair, ok := <-candidatePairCh:
			// Received event for a specific candidate pair.
			if !ok {
				return webrtc.ICECandidatePairStats{}, false
			}

			if pair.State == webrtc.StatsICECandidatePairStateSucceeded {
				return pair, true
			}
			// If the candidate pair is still in progress, let's wait more.

		case state, ok := <-connectionStateCh:
			// Received event for a connection state.
			if !ok {
				return webrtc.ICECandidatePairStats{}, false
			}
			if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed {
				return webrtc.ICECandidatePairStats{}, false
			}

		case <-disabledCh:
			// When peer marked as Disabled, basically a Failed/Closed state.
			return webrtc.ICECandidatePairStats{}, false
		}
	}
}

func (p *PeerManager) onPeerCandidatesGathered(remotePeer uint) onPeerCandidatesGatheredCallback {
	return func(description *webrtc.SessionDescription, candidates []webrtc.ICECandidate) {
		err := p.icebreakerClient.SendEvent(
			icebreaker.CandidatesMessage{
				BaseEvent: icebreaker.BaseEvent{
					EventType:   "candidates",
					GameID:      p.gameId,
					SenderID:    p.localUserId,
					RecipientID: &remotePeer,
				},
				Session:    description,
				Candidates: candidates,
			})

		if err != nil {
			applog.Error("Failed to send candidates",
				zap.Uint("playerId", p.localUserId),
				zap.Error(err),
			)
		}
	}
}

func (p *PeerManager) onPeerConnected(peer *Peer, remoteAddr *net.IPAddr) {
	p.StoreAllowedAddr(remoteAddr)
	p.peerMu.Lock()
	defer p.peerMu.Unlock()
	for _, localPeer := range p.peers {
		if localPeer.peerId == peer.peerId {
			continue
		}

		localPeer.gameDataProxy.UpdateAllowedAddresses(p.peerAddressesV4, p.peerAddressesV6)
	}
}

func (p *PeerManager) onPeerDisconnected(peer *Peer) {
	if peer.remoteAddress != nil {
		applog.FromContext(peer.context).Debug(
			"Removing failed/closed peer address from UDP allowed list",
			zap.String("remoteAddress", peer.remoteAddress.String()),
		)
		p.RemoveAllowedAddr(peer.remoteAddress)
	}

	p.peerMu.Lock()
	defer p.peerMu.Unlock()
	for _, localPeer := range p.peers {
		if localPeer.peerId == peer.peerId {
			continue
		}

		localPeer.gameDataProxy.UpdateAllowedAddresses(p.peerAddressesV4, p.peerAddressesV6)
	}
}
