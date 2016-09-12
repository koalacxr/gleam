package util

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"sync"

	"gopkg.in/vmihailenco/msgpack.v2"
)

/*
On the wire Message format, pipe:
  32 bits byte length
  []byte encoded in msgpack format

Channel Message format:
  []byte
    consequtive sections of []byte, each section is an object encoded in msgpack format

	This is not actually an array object,
	but just a consequtive list of encoded bytes for each object,
	because msgpack can sequentially decode the objects

When used by Shell scripts:
  from input channel:
    decode the msgpack-encoded []byte into strings that's tab and '\n' separated
	and feed into the shell script
  to output channel:
    encode the tab and '\n' separated lines into msgpack-format []byte
	and feed into the output channel

When used by Lua scripts:
  from input channel:
    decode the msgpack-encoded []byte into array of objects
	and pass these objects as function parameters
  to output channel:
    encode returned objects as an array of objects, into msgpack encoded []byte
	and feed into the output channel

Output Message format:
  decoded objects

Lua scripts need to decode the input and encode the output in msgpack format.
Go code also need to decode the input to "see" the data, e.g. Sort(),
and encode the output, e.g. Source().

Shell scripts via Pipe should see clear data, so the
*/

// setup asynchronously to merge multiple channels into one channel
func MergeChannel(cs []chan []byte, out chan []byte) {
	var wg sync.WaitGroup

	for _, c := range cs {
		wg.Add(1)
		go func(c chan []byte) {
			defer wg.Done()
			for n := range c {
				out <- n
			}
		}(c)
	}

	go func() {
		wg.Wait()
		close(out)
	}()
}

func LinkChannel(wg *sync.WaitGroup, inChan, outChan chan []byte) {
	wg.Add(1)
	defer wg.Done()
	for bytes := range inChan {
		outChan <- bytes
	}
	close(outChan)
}

func ReaderToChannel(wg *sync.WaitGroup, name string, reader io.ReadCloser, ch chan []byte, errorOutput io.Writer) {
	defer wg.Done()
	defer reader.Close()
	defer close(ch)

	var length int32

	for {
		err := binary.Read(reader, binary.LittleEndian, &length)
		if err == io.EOF {
			break
		}
		if err != nil {
			// getting this: FlatMap>Failed to read from input to channel: read |0: bad file descriptor
			fmt.Fprintf(errorOutput, "%s>Failed to read bytes length from input to channel: %v\n", name, err)
			break
		}
		if length == 0 {
			continue
		}
		data := make([]byte, length)
		_, err = io.ReadFull(reader, data)
		if err == io.EOF {
			fmt.Fprintf(errorOutput, "%s>Getting EOF from reader to channel: %v\n", name, err)
			break // this is not really correct, but stop anyway
		}
		if err != nil {
			fmt.Fprintf(errorOutput, "%s>Getting error from reader to channel: %v\n", name, err)
			break // this is not really correct, but stop anyway
		}
		// this is output from FlatMap to the output
		// println(name + " reader -> chan output data:" + string(data))
		ch <- data
	}
}

func ChannelToWriter(wg *sync.WaitGroup, name string, ch chan []byte, writer io.WriteCloser, errorOutput io.Writer) {
	defer wg.Done()
	defer writer.Close()

	for bytes := range ch {
		// println(name + " chan -> writer input data:" + string(bytes))
		if err := binary.Write(writer, binary.LittleEndian, int32(len(bytes))); err != nil {
			fmt.Fprintf(errorOutput, "%s>Failed to write length of bytes from channel to writer: %v\n", name, err)
			//return
		}
		if _, err := writer.Write(bytes); err != nil {
			fmt.Fprintf(errorOutput, "%s>Failed to write bytes from channel to writer: %v\n", name, err)
			//return
		}
	}
}

func LineReaderToChannel(wg *sync.WaitGroup, name string, reader io.ReadCloser, ch chan []byte, errorOutput io.Writer) {
	defer wg.Done()
	defer reader.Close()
	defer close(ch)

	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		// fmt.Printf("line input: %s\n", scanner.Text())
		parts := bytes.Split(scanner.Bytes(), []byte{'\t'})
		var buf bytes.Buffer
		encoder := msgpack.NewEncoder(&buf)
		for _, p := range parts {
			if err := encoder.Encode(p); err != nil {
				if err != nil {
					fmt.Fprintf(errorOutput, "%s>Failed to encode bytes from channel to writer: %v\n", name, err)
					return
				}
			}
		}
		ch <- buf.Bytes()
	}
	if err := scanner.Err(); err != nil {
		// TODO: what's wrong here?
		// seems the program could have ended when reading the output.
		// fmt.Fprintf(errorOutput, "Failed to read from input to channel: %v\n", err)
	}
}

func ChannelToLineWriter(wg *sync.WaitGroup, name string, ch chan []byte, writer io.WriteCloser, errorOutput io.Writer) {
	defer wg.Done()
	defer writer.Close()

	if err := FprintRowsFromChannel(writer, ch, "\t", "\n"); err != nil {
		fmt.Fprintf(errorOutput, "%s>Failed to decode bytes from channel to writer: %v\n", name, err)
		return
	}
}