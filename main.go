package main

import (
	"faf-pioneer/forgedalliance"
	"faf-pioneer/icebreaker"
	"faf-pioneer/webrtc"
	"flag"
	"fmt"
	pionwebrtc "github.com/pion/webrtc/v4"
	"log"
	"strings"
)

type GlobalChannels struct {
	gpgNetFromGame      chan *forgedalliance.GpgMessage
	gpgNetToGame        chan *forgedalliance.GpgMessage
	gpgNetToFafClient   chan *forgedalliance.GpgMessage
	gpgNetFromFafClient chan *forgedalliance.GpgMessage
	gameDataToGame      chan *[]byte
}

func main() {
	// Define flags without default values (not passing a value will cause an error)
	userId := flag.Uint("user-id", 0, "The ID of the user")
	gameId := flag.Uint64("game-id", 0, "The ID of the game session")
	accessToken := flag.String("access-token", "", "The access token for authentication")
	apiRoot := flag.String("api-root", "https://api.faforever.com/ice", "The root uri of the icebreaker api")
	gpgNetPort := flag.Uint("gpgnet-port", 0, "The port which the game will connect to for exchanging GgpNet messages")
	gpgNetClientPort := flag.Uint("gpgnet-client-port", 0, "The port which on which the parent FAF client listens on")
	gameUdpPort := flag.Uint("game-udp-port", 0, "The port which the game will send/receive game data")

	// Parse the command-line flags
	flag.Parse()

	// Validate that the required flags are provided
	if *userId == 0 {
		log.Fatalf("Error: --user-id is required and must be a valid uint32.")
	}

	if *gameId == 0 {
		log.Fatalf("Error: --game-id is required and must be a valid uint64.")
	}

	if *accessToken == "" {
		log.Fatalf("Error: --access-token is required and cannot be empty.")
	}

	if *gpgNetPort == 0 {
		log.Fatalf("Error: --gpgnet-port is required and cannot be empty.")
	}

	globalChannels := GlobalChannels{
		gpgNetFromGame:      make(chan *forgedalliance.GpgMessage),
		gpgNetToGame:        make(chan *forgedalliance.GpgMessage),
		gpgNetToFafClient:   make(chan *forgedalliance.GpgMessage),
		gpgNetFromFafClient: make(chan *forgedalliance.GpgMessage),
		gameDataToGame:      make(chan *[]byte),
	}

	// Wire GpgNetClient to GpgNetServer
	go func() {
		for msg := range globalChannels.gpgNetFromGame {
			globalChannels.gpgNetToFafClient <- msg
		}
	}()

	go func() {
		for msg := range globalChannels.gpgNetFromFafClient {
			globalChannels.gpgNetToGame <- msg
		}
	}()

	// Start the Gpgnet Control Server
	gpgNetServer := forgedalliance.NewGpgNetServer(*gpgNetPort)
	go gpgNetServer.Listen(globalChannels.gpgNetFromGame, globalChannels.gpgNetToGame)

	// Start the GpgNet client to proxy data to the FAF client
	gpgNetClient := forgedalliance.NewGpgNetClient(*gpgNetClientPort)
	go gpgNetClient.Listen(globalChannels.gpgNetToFafClient, globalChannels.gpgNetFromFafClient)

	// Gather ICE servers and listen for WebRTC events
	icebreakerClient := icebreaker.NewClient(*apiRoot, *gameId, *accessToken)
	sessionGameResponse, err := icebreakerClient.GetGameSession()

	if err != nil {
		log.Fatal("Could not query turn servers: ", err)
	}

	channel := make(chan icebreaker.EventMessage)
	go icebreakerClient.Listen(channel)

	turnServer := make([]pionwebrtc.ICEServer, len(sessionGameResponse.Servers))
	for i, server := range sessionGameResponse.Servers {
		turnServer[i] = pionwebrtc.ICEServer{
			Username:       server.Username,
			Credential:     server.Credential,
			CredentialType: pionwebrtc.ICECredentialTypePassword,
			URLs:           make([]string, len(server.Urls)),
		}

		for j, url := range server.Urls {
			// for Java being Java reasons we unfortunately raped the URLs and need to convert it back
			turnServer[i].URLs[j] = strings.ReplaceAll(url, "://", ":")
		}
	}

	// WebRTC setup
	peers := make(map[uint]*webrtc.Peer)

	var peerUdpPort uint = 18000 // TODO: Pick a "free random one"

	for msg := range channel {
		switch event := msg.(type) {
		case *icebreaker.ConnectedMessage:
			log.Printf("Connecting to peer: %s\n", event)

			peer, err := webrtc.CreatePeer(true, event.SenderID, turnServer, peerUdpPort, *gameUdpPort, func(description *pionwebrtc.SessionDescription, candidates []*pionwebrtc.ICECandidate) {
				err := icebreakerClient.SendEvent(
					icebreaker.CandidatesMessage{
						EventType:   "candidates",
						GameID:      *gameId,
						SenderID:    *userId,
						RecipientID: &event.SenderID,
						Session:     description,
						Candidates:  candidates,
					})

				if err != nil {
					log.Printf("Failed to send candidates: %s\n", err)
				}
			})

			if err != nil {
				panic(err)
			}

			peers[event.SenderID] = peer
			peerUdpPort++
		case *icebreaker.CandidatesMessage:
			fmt.Printf("Received CandidatesMessage: %s\n", event)
			peer := peers[event.SenderID]

			if peer == nil {
				peer, err = webrtc.CreatePeer(false, event.SenderID, turnServer, peerUdpPort, *gameUdpPort, func(description *pionwebrtc.SessionDescription, candidates []*pionwebrtc.ICECandidate) {
					err := icebreakerClient.SendEvent(
						icebreaker.CandidatesMessage{
							EventType:   "candidates",
							GameID:      *gameId,
							SenderID:    *userId,
							RecipientID: &event.SenderID,
							Session:     description,
							Candidates:  candidates,
						})

					if err != nil {
						log.Printf("Failed to send candidates: %s\n", err)
					}
				})
				if err != nil {
					panic(err)
				}

				peers[event.SenderID] = peer
				peerUdpPort++
			}

			err := peer.AddCandidates(event.Session, event.Candidates)
			if err != nil {
				panic(err)
			}
		default:
			fmt.Printf("Unknown event type: %s\n", event)
		}
	}
}
