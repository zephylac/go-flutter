package plugin

import (
	"fmt"
	"runtime/debug"
	"sync"

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
	stream := Stream{
		stopCh:       make(chan struct{}),
		publishCh:    make(chan interface{}, 1),
		subCh:        make(chan chan interface{}, 1),
		unsubCh:      make(chan chan interface{}, 1),
		eventChannel: *e,
		arguments:    arguments,
	}

	stream.Start()

	return &stream
}

// Start ...
func (b *Stream) Start() {
	subs := map[chan interface{}]struct{}{}
	for {
		methodChannel := NewMethodChannel(b.eventChannel.messenger, b.eventChannel.channelName, b.eventChannel.methodCodec)

		select {
		case <-b.stopCh:
			return
		case msgCh := <-b.subCh:
			// If this is the first listener to subscribe / setup stream
			if len(subs) == 0 {
				fmt.Printf("first one to subscribe\n")
				methodChannel.InvokeMethod("listen", b.arguments)
			}
			subs[msgCh] = struct{}{}
		case msgCh := <-b.unsubCh:
			// If this is the last listener to unsubscribe / tear down stream
			if len(subs) == 1 {
				fmt.Printf("last one to unsubscribe\n")
				methodChannel.InvokeMethod("cancel", b.arguments)
			}
			delete(subs, msgCh)
		case msg := <-b.publishCh:
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
func (b *Stream) Stop() {
	close(b.stopCh)
}

// Subscribe ...
func (b *Stream) Subscribe() chan interface{} {
	msgCh := make(chan interface{}, 5)
	b.subCh <- msgCh
	return msgCh
}

// Unsubscribe ...
func (b *Stream) Unsubscribe(msgCh chan interface{}) {
	b.unsubCh <- msgCh
}

// Publish ...
func (b *Stream) Publish(msg interface{}) {
	b.publishCh <- msg
}

// EventChannel defines a channel for communicating with platform plugins using event streams.
type EventChannel struct {
	messenger   BinaryMessenger
	channelName string
	methodCodec MethodCodec

	methods     map[string]methodHandlerRegistration
	methodsLock sync.RWMutex
}

// NewEventChannel creates a new method channel
func NewEventChannel(messenger BinaryMessenger, channelName string, methodCodec MethodCodec) (channel *EventChannel) {
	mc := &EventChannel{
		messenger:   messenger,
		channelName: channelName,
		methodCodec: methodCodec,

		methods: make(map[string]methodHandlerRegistration),
	}
	messenger.SetChannelHandler(channelName, mc.handleChannelMessage)

	return mc
}

// ReceiveBroadcastStream ...
func (e *EventChannel) ReceiveBroadcastStream(arguments interface{}) *Stream {

	stream := *NewStream(e, arguments)

	/* if methodCall.Method == "listen" {
		//onListen(call.arguments, reply);
	} else if methodCall.Method == "cancel" {
		//onCancel(call.arguments, reply);
	} else {
		//reply.reply(null)
	} */

	/*
		clientFunc := func(id int) {
			msgCh := b.Subscribe()
			for {
				fmt.Printf("Client %d got message: %v\n", id, <-msgCh)
			}
		}
	*/

	return &stream
}

// handleChannelMessage decodes incoming binary message to a method call, calls the
// handler, and encodes the outgoing reply.
func (e *EventChannel) handleChannelMessage(binaryMessage []byte, responseSender ResponseSender) (err error) {
	methodCall, err := e.methodCodec.DecodeMethodCall(binaryMessage)
	if err != nil {
		return errors.Wrap(err, "failed to decode incomming message")
	}

	e.methodsLock.RLock()
	registration, registrationExists := e.methods[methodCall.Method]
	e.methodsLock.RUnlock()
	if !registrationExists {
		fmt.Printf("go-flutter: no method handler registered for method '%s' on channel '%s'\n", methodCall.Method, e.channelName)
		responseSender.Send(nil)
		return nil
	}

	if registration.sync {
		e.handleMethodCall(registration.handler, methodCall, responseSender)
	} else {
		go e.handleMethodCall(registration.handler, methodCall, responseSender)
	}

	return nil
}

// handleMethodCall handles the methodcall and sends a response.
func (e *EventChannel) handleMethodCall(handler MethodHandler, methodCall MethodCall, responseSender ResponseSender) {
	defer func() {
		p := recover()
		if p != nil {
			fmt.Printf("go-flutter: recovered from panic while handling call for method '%s' on channel '%s': %v\n", methodCall.Method, e.channelName, p)
			debug.PrintStack()
		}
	}()

	reply, err := handler.HandleMethod(methodCall.Arguments)
	if err != nil {
		fmt.Printf("go-flutter: handler for method '%s' on channel '%s' returned an error: %v\n", methodCall.Method, e.channelName, err)
		binaryReply, err := e.methodCodec.EncodeErrorEnvelope("error", err.Error(), nil)
		if err != nil {
			fmt.Printf("go-flutter: failed to encode error envelope for method '%s' on channel '%s', error: %v\n", methodCall.Method, e.channelName, err)
		}
		responseSender.Send(binaryReply)
	}

	binaryReply, err := e.methodCodec.EncodeSuccessEnvelope(reply)
	if err != nil {
		fmt.Printf("go-flutter: failed to encode success envelope for method '%s' on channel '%s', error: %v\n", methodCall.Method, e.channelName, err)
	}
	responseSender.Send(binaryReply)
}
