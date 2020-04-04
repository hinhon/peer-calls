package signals

import (
	"fmt"

	"github.com/jeremija/peer-calls/src/server-go/logger"
	"github.com/jeremija/peer-calls/src/server-go/wrtc/negotiator"
	"github.com/pion/webrtc/v2"
)

type PeerConnection interface {
	OnICECandidate(func(*webrtc.ICECandidate))
	OnSignalingStateChange(func(webrtc.SignalingState))
	AddICECandidate(webrtc.ICECandidateInit) error
	AddTransceiverFromKind(codecType webrtc.RTPCodecType, init ...webrtc.RtpTransceiverInit) (*webrtc.RTPTransceiver, error)
	SetRemoteDescription(webrtc.SessionDescription) error
	SetLocalDescription(webrtc.SessionDescription) error
	CreateOffer(*webrtc.OfferOptions) (webrtc.SessionDescription, error)
	CreateAnswer(*webrtc.AnswerOptions) (webrtc.SessionDescription, error)
}

type Signaller struct {
	peerConnection PeerConnection
	mediaEngine    *webrtc.MediaEngine
	initiator      bool
	localPeerID    string
	remotePeerID   string
	onSignal       func(signal interface{})
	negotiator     *negotiator.Negotiator
}

var log = logger.GetLogger("signals")
var sdpLog = logger.GetLogger("sdp")

func NewSignaller(
	initiator bool,
	peerConnection PeerConnection,
	mediaEngine *webrtc.MediaEngine,
	localPeerID string,
	remotePeerID string,
	onSignal func(signal interface{}),
) (*Signaller, error) {
	s := &Signaller{
		initiator:      initiator,
		peerConnection: peerConnection,
		mediaEngine:    mediaEngine,
		localPeerID:    localPeerID,
		remotePeerID:   remotePeerID,
		onSignal:       onSignal,
	}

	negotiator := negotiator.NewNegotiator(
		initiator,
		peerConnection,
		s.remotePeerID,
		s.handleLocalOffer,
		s.handleLocalRequestNegotiation,
	)

	s.negotiator = negotiator

	// peerConnection.OnICECandidate(s.handleICECandidate)

	return s, s.initialize()
}

func (s *Signaller) initialize() error {
	if !s.initiator {
		log.Printf("[%s] NewSignaller: Non-Initiator add recvonly transceiver", s.remotePeerID)
		_, err := s.peerConnection.AddTransceiverFromKind(
			webrtc.RTPCodecTypeVideo,
			webrtc.RtpTransceiverInit{
				Direction: webrtc.RTPTransceiverDirectionSendrecv,
			},
		)
		if err != nil {
			return fmt.Errorf("[%s] NewSignaller: Error adding video transceiver: %s", s.remotePeerID, err)
		}
		// // TODO add one more video transceiver for screen sharing
		// // TODO add audio
		// _, err = peerConnection.AddTransceiverFromKind(
		// 	webrtc.RTPCodecTypeAudio,
		// 	webrtc.RtpTransceiverInit{
		// 		Direction: webrtc.RTPTransceiverDirectionRecvonly,
		// 	},
		// )
		// if err != nil {
		// 	log.Printf("[%s] Error adding audio transceiver: %s", err)
		// 	w.WriteHeader(http.StatusInternalServerError)
		// 	return
		// }
	} else {
		log.Printf("[%s] NewSignaller: Initiator registering default codecs", s.remotePeerID)
		s.mediaEngine.RegisterDefaultCodecs()
		log.Printf("[%s] NewSignaller: Initiator calling Negotiate()", s.remotePeerID)
		s.negotiator.Negotiate()
	}

	return nil
}

func (s *Signaller) Initiator() bool {
	return s.initiator
}

func (s *Signaller) handleICECandidate(c *webrtc.ICECandidate) {
	if c == nil {
		return
	}

	payload := Payload{
		UserID: s.localPeerID,
		Signal: Candidate{
			Candidate: c.ToJSON(),
		},
	}

	log.Printf("[%s] Got ice candidate from server peer: %s", payload, s.remotePeerID)
	s.onSignal(payload)
}

func (s *Signaller) Signal(payload map[string]interface{}) error {
	signalPayload, err := NewPayloadFromMap(payload)

	if err != nil {
		return fmt.Errorf("Error constructing signal from payload: %s", err)
	}

	switch signal := signalPayload.Signal.(type) {
	case Candidate:
		log.Printf("[%s] Remote signal.canidate: %s ", signal.Candidate, s.remotePeerID)
		return s.peerConnection.AddICECandidate(signal.Candidate)
	case Renegotiate:
		log.Printf("[%s] Remote signal.renegotiate ", s.remotePeerID)
		log.Printf("[%s] Calling signaller.Negotiate() because remote peer wanted to negotiate", s.remotePeerID)
		s.Negotiate()
		return nil
	case TransceiverRequest:
		log.Printf("[%s] Remote signal.transceiverRequest: %s", s.remotePeerID, signal.TransceiverRequest.Kind)
		return s.handleTransceiverRequest(signal)
	case webrtc.SessionDescription:
		sdpLog.Printf("[%s] Remote signal.type: %s, signal.sdp: %s", s.remotePeerID, signal.Type, signal.SDP)
		return s.handleRemoteSDP(signal)
	default:
		return fmt.Errorf("[%s] Unexpected signal: %#v ", s.remotePeerID, signal)
	}
}

func (s *Signaller) handleTransceiverRequest(transceiverRequest TransceiverRequest) (err error) {
	log.Printf("[%s] Add recvonly transceiver per request %v", s.remotePeerID, transceiverRequest)

	codecType := transceiverRequest.TransceiverRequest.Kind
	var t *webrtc.RTPTransceiver
	// if init := transceiverRequest.TransceiverRequest.Init; init != nil {
	// 	t, err = s.peerConnection.AddTransceiverFromKind(codecType, *init)
	// } else {
	t, err = s.peerConnection.AddTransceiverFromKind(
		codecType,
		webrtc.RtpTransceiverInit{
			Direction: webrtc.RTPTransceiverDirectionRecvonly,
		},
	)
	// }
	log.Printf("[%s] Added %s transceiver, direction: %s", s.remotePeerID, codecType, t.Direction())

	if err != nil {
		return fmt.Errorf("[%s] Error adding transceiver type %s: %s", s.remotePeerID, codecType, err)
	}

	log.Printf("[%s] Calling signaller.Negotiate() because a new transceiver was added", s.remotePeerID)
	s.Negotiate()
	return nil
}

func (s *Signaller) handleRemoteSDP(sessionDescription webrtc.SessionDescription) (err error) {
	switch sessionDescription.Type {
	case webrtc.SDPTypeOffer:
		return s.handleRemoteOffer(sessionDescription)
	case webrtc.SDPTypeAnswer:
		return s.handleRemoteAnswer(sessionDescription)
	default:
		return fmt.Errorf("[%s] Unexpected sdp type: %s", s.remotePeerID, sessionDescription.Type)
	}
}

func (s *Signaller) handleRemoteOffer(sessionDescription webrtc.SessionDescription) (err error) {
	if err = s.mediaEngine.PopulateFromSDP(sessionDescription); err != nil {
		return fmt.Errorf("[%s] Error populating codec info from SDP: %s", s.remotePeerID, err)
	}

	if err = s.peerConnection.SetRemoteDescription(sessionDescription); err != nil {
		return fmt.Errorf("[%s] Error setting remote description: %w", s.remotePeerID, err)
	}
	answer, err := s.peerConnection.CreateAnswer(nil)
	if err != nil {
		return fmt.Errorf("[%s] Error creating answer: %w", s.remotePeerID, err)
	}
	if err := s.peerConnection.SetLocalDescription(answer); err != nil {
		return fmt.Errorf("[%s] Error setting local description: %w", s.remotePeerID, err)
	}

	sdpLog.Printf("[%s] Local signal.type: %s, signal.sdp: %s", s.remotePeerID, answer.Type, answer.SDP)
	s.onSignal(NewPayloadSDP(s.localPeerID, answer))
	return nil
}

func (s *Signaller) handleLocalRequestNegotiation() {
	log.Printf("[%s] Sending renegotiation request to initiator", s.remotePeerID)
	s.onSignal(NewPayloadRenegotiate(s.localPeerID))
}

func (s *Signaller) handleLocalOffer(offer webrtc.SessionDescription, err error) {
	sdpLog.Printf("[%s] Local signal.type: %s, signal.sdp: %s", s.remotePeerID, offer.Type, offer.SDP)
	if err != nil {
		log.Printf("[%s] Error creating local offer: %s", s.remotePeerID, err)
		// TODO abort connection
		return
	}

	err = s.peerConnection.SetLocalDescription(offer)
	if err != nil {
		log.Printf("[%s] Error setting local description from local offer: %s", s.remotePeerID, err)
		// TODO abort connection
		return
	}

	s.onSignal(NewPayloadSDP(s.localPeerID, offer))
}

// Sends a request for a new transceiver, only if the peer is not the initiator.
func (s *Signaller) SendTransceiverRequest(kind webrtc.RTPCodecType, direction webrtc.RTPTransceiverDirection) {
	if !s.initiator {
		log.Printf("[%s] Sending transceiver request to initiator", s.remotePeerID)
		s.onSignal(NewTransceiverRequest(s.localPeerID, kind, direction))
	}
}

// TODO check offer voice activation detection feature of webrtc

// Create an offer and send it to remote peer
func (s *Signaller) Negotiate() {
	s.negotiator.Negotiate()
}

func (s *Signaller) handleRemoteAnswer(sessionDescription webrtc.SessionDescription) (err error) {
	if err = s.peerConnection.SetRemoteDescription(sessionDescription); err != nil {
		return fmt.Errorf("[%s] Error setting remote description: %w", s.remotePeerID, err)
	}
	return nil
}
