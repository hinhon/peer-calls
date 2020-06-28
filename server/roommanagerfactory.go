package server

type RoomManagerFactory struct {
	params *RoomManagerFactoryParams
	logger Logger
}

type RoomManagerFactoryParams struct {
	Config         NetworkConfig
	AdapterFactory *AdapterFactory
	TracksManager  TracksManager
	LoggerFactory  LoggerFactory
}

func NewRoomManagerFactory(params RoomManagerFactoryParams) *RoomManagerFactory {
	return &RoomManagerFactory{
		params: &params,
		logger: params.LoggerFactory.GetLogger("roommanagerfactory"),
	}
}

func (rmf *RoomManagerFactory) NewRoomManager() (RoomManager, *NodeManager) {
	c := rmf.params.Config

	rooms := NewAdapterRoomManager(rmf.params.AdapterFactory.NewAdapter)

	if c.Type == NetworkTypeSFU && c.SFU.Transport.ListenAddr != "" {
		roomManager, nodeManager, err := rmf.createChannelRoomManager(rooms)
		if err == nil {
			return roomManager, nodeManager
		}
		rmf.logger.Println("Error creating NodeTransport, falling back to single SFU")
	}

	return rooms, nil
}

func (rmf *RoomManagerFactory) createChannelRoomManager(
	rooms RoomManager,
) (*ChannelRoomManager, *NodeManager, error) {
	c := rmf.params.Config

	listenAddr, err := ParseUDPAddr(c.SFU.Transport.ListenAddr)
	if err != nil {
		return nil, nil, err
	}

	nodes, err := ParseUDPAddrs(c.SFU.Transport.Nodes)
	if err != nil {
		return nil, nil, err
	}

	channelRoomManager := NewChannelRoomManager(rooms)

	nodeManager, err := NewNodeManager(NodeManagerParams{
		LoggerFactory: rmf.params.LoggerFactory,
		ListenAddr:     listenAddr,
		Nodes:         nodes,
		RoomManager:   channelRoomManager,
		TracksManager: rmf.params.TracksManager,
	})

	if err != nil {
		channelRoomManager.Close()
		return nil, nil, err
	}

	return channelRoomManager, nodeManager, nil
}