package plugin

import (
	"fmt"
	"runtime/debug"

	"github.com/pkg/errors"
)

// Stream ...
type Stream struct {
	stopCh       chan struct{}
	publishCh    chan interface{}
	subCh        chan chan interface{}
	unsubCh      chan chan interface{}
	eventChannel EventChannel
	arguments    interface{}
}

// NewStream ...
func NewStream(e *EventChannel, arguments interface{}) *Stream {
	e.stream = &Stream{
		stopCh:    make(chan struct{}),
		publishCh: make(chan interface{}, 1),
		subCh:     make(chan chan interface{}, 1),
		unsubCh:   make(chan chan interface{}, 1),
		arguments: arguments,
	}

	e.Start()

	return e.stream
}

// Start ...
func (e *EventChannel) Start() {
	subs := map[chan interface{}]struct{}{}
	for {
		methodChannel := NewMethodChannel(e.messenger, e.channelName, e.methodCodec)

		select {
		case <-e.stream.stopCh:
			return
		case msgCh := <-e.stream.subCh:
			// If this is the first listener to subscribe / setup stream
			if len(subs) == 0 {
				fmt.Printf("first one to subscribe\n")
				methodChannel.InvokeMethod("listen", e.stream.arguments)
			}
			subs[msgCh] = struct{}{}
		case msgCh := <-e.stream.unsubCh:
			// If this is the last listener to unsubscribe / tear down stream
			if len(subs) == 1 {
				fmt.Printf("last one to unsubscribe\n")
				methodChannel.InvokeMethod("cancel", e.stream.arguments)
			}
			delete(subs, msgCh)
		case msg := <-e.stream.publishCh:
			for msgCh := range subs {
				// msgCh is buffered, use non-blocking send to protect the stream:
				select {
				case msgCh <- msg:
				default:
				}
			}
		}
	}
}

// Stop ...
func (e *EventChannel) Stop() {
	close(e.stream.stopCh)
}

// Subscribe ...
func (e *EventChannel) Subscribe() chan interface{} {
	msgCh := make(chan interface{}, 5)
	e.stream.subCh <- msgCh
	return msgCh
}

// Unsubscribe ...
func (e *EventChannel) Unsubscribe(msgCh chan interface{}) {
	e.stream.unsubCh <- msgCh
}

// Publish ...
func (e *EventChannel) Publish(msg interface{}) {
	e.stream.publishCh <- msg
}

// EventChannel defines a channel for communicating with platform plugins using event streams.
type EventChannel struct {
	messenger   BinaryMessenger
	channelName string
	methodCodec MethodCodec

	stream *Stream
}

// NewEventChannel creates a new method channel
func NewEventChannel(messenger BinaryMessenger, channelName string, methodCodec MethodCodec) (channel *EventChannel) {
	mc := &EventChannel{
		messenger:   messenger,
		channelName: channelName,
		methodCodec: methodCodec,
	}
	return mc
}

// ReceiveBroadcastStream ...
func (e *EventChannel) ReceiveBroadcastStream(arguments interface{}) *Stream {

	stream := *NewStream(e, arguments)

	e.messenger.SetChannelHandler(e.channelName, e.handleChannelMessage)

	return &stream
}

// handleChannelMessage decodes incoming binary message to a method call, calls the
// handler, and encodes the outgoing reply.
func (e *EventChannel) handleChannelMessage(binaryMessage []byte, responseSender ResponseSender) (err error) {
	envelope, err := e.methodCodec.DecodeEnvelope(binaryMessage)
	if err != nil {
		return errors.Wrap(err, "failed to decode incomming message")
	}

	go e.handleMethodCall(envelope)

	return nil
}

// handleMethodCall handles the methodcall and sends a response.
func (e *EventChannel) handleMethodCall(envelope interface{}) {
	defer func() {
		p := recover()
		if p != nil {
			fmt.Printf("go-flutter: recovered from panic while handling event for envelope '%s' on channel '%s': %v\n", envelope, e.channelName, p)
			debug.PrintStack()
		}
	}()

	binaryReply, err := e.methodCodec.EncodeSuccessEnvelope(envelope)
	if err != nil {
		fmt.Printf("go-flutter: failed to encode success envelope for event '%s' on channel '%s', error: %v\n", envelope, e.channelName, err)
	}
	e.Publish(binaryReply)
}
