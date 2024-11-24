package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

var debug bool = false
var useBackingBlocks bool = false
var pageSize int64
var numFrames int64
var numPages int64
var numBackingBlocks int64
var reader *bufio.Reader

type MMU interface {
	// Write(pageNum int64, offset int64)
	// Read(pageNum int64, offset int64)
	Access(pageNum int64, offset int64, isWrite bool)
	Init()
}

// struct for a virtual page
type page struct {
	pfn       int64 // the corresponding physical frame number. -2 if never mapped, -1 if mapped and stolen.
	inSwap    bool
	dirty     bool
	onDisk    bool // only true if this page has been written to in the past
	lastUsed  int64
	firstUsed int64
}

type page_metadata struct {
	pageTable               []*page
	pageFrames              []int64
	numMapped               int64
	numReferenced           int64
	numMissed               int64
	numStolen               int64
	numWrittenToSwap        int64
	numRecoveredFromSwapped int64
}

type FIFO struct {
	nxt_idx chan int64 // PFN that is to be popped next
}

type LRUEntry struct {
	my_page *page
	nxt     *LRUEntry
	prev    *LRUEntry
}

// could've implemented LRU by searching through the pages to find the LRU entry.
// but that would mean O(n) search every time.
// use a linked list with a node table instead. nodeTable[i] = pointer to the page i in the linked list.
// Extra O(n) space, but O(1) search & update.
type LRU struct {
	head      *LRUEntry
	tail      *LRUEntry
	nodeTable []*LRUEntry // given a page number, return its LRUEntry pointer
	size      int64
}

type RefListEntry struct {
	timeRef int64
	nxt     *RefListEntry
}

type RefList struct {
	head *RefListEntry
	tail *RefListEntry
}

type OpEntry struct {
	pageNum int64
	offset  int64
	opType  int // 0 for read, 1 for write, 2 for enable deubgging, 3 for disable debugging, 4 for printMetadata()
	nxt     *OpEntry
}

type OPTIMAL struct {
	refList          []*RefList // nxtRef[i] = linked list of reference times for page i
	head             *OpEntry   // head of linked list of page operations to perform
	tail             *OpEntry
	size             int64
	maxFrameIndex    int64 // frame index which has the page that will be used the furthest in the future out of all the current frames
	maxFrameTime     int64 // next use time of page at the frame maxFrameIndex
	nxtMaxFrameIndex int64 // for second furthest, similar to maxFrameIndex
	nxtMaxFrameTime  int64 // for second furthest, similar to maxFrameIndex

}

var data page_metadata // holds page table, page frames, and other metadata
var cnt int64 = 1

func (mmu *FIFO) Init() {

}

func (mmu *FIFO) Access(pageNum int64, offset int64, isWrite bool) {
	var frameIndex int64

	pfn := data.pageTable[pageNum].pfn
	if pfn < 0 { // page is currently not in a frame, put it there

		// frame is full, steal a page.
		if int64(len(mmu.nxt_idx)) == numFrames {
			frameIndex = <-mmu.nxt_idx
			replace_pageNum := data.pageFrames[frameIndex] // virtual page number to be replaced
			if data.pageTable[replace_pageNum].dirty {     // if the page to be replaced is dirty, write it to swap
				data.pageTable[replace_pageNum].dirty = false
				data.pageTable[replace_pageNum].onDisk = true
				data.numWrittenToSwap++
			}
			data.pageTable[replace_pageNum].inSwap = true
			data.pageTable[replace_pageNum].pfn = -1
			data.numStolen++
		} else { // if len(mmu.nxt_idx) < numFrames, we still have space in the frame, use it instead of stealing pages
			frameIndex = int64(len(mmu.nxt_idx))
		}

		if pfn == -2 { // page to write has never been mapped before
			data.numMapped++
		} else {
			if data.pageTable[pageNum].inSwap { // check if it's in swap
				if data.pageTable[pageNum].onDisk { // only true if this page has been written to in the past
					data.numRecoveredFromSwapped++
				}
				data.pageTable[pageNum].inSwap = false
			}
		}
		data.pageTable[pageNum].pfn = frameIndex
		if isWrite {
			data.pageTable[pageNum].dirty = true
			data.pageTable[pageNum].onDisk = false
		}
		data.pageTable[pageNum].firstUsed = cnt
		data.pageFrames[frameIndex] = pageNum
		data.numMissed++
		mmu.nxt_idx <- frameIndex
	} else { // page is currently in a frame
		if isWrite {
			data.pageTable[pageNum].dirty = true
		}
	}
	data.pageTable[pageNum].lastUsed = cnt
	data.numReferenced++
}

func (mmu *LRU) Init() {
	mmu.nodeTable = make([]*LRUEntry, numPages)
	mmu.head = nil
	mmu.tail = nil
	mmu.size = 0
	var i int64
	for i = 0; i < numPages; i++ {
		mmu.nodeTable[i] = nil
	}
}

func (mmu *LRU) Access(pageNum int64, offset int64, isWrite bool) {
	var frameIndex int64
	var node *LRUEntry

	defer func() {
		data.pageTable[pageNum].lastUsed = cnt
		mmu.nodeTable[pageNum] = node
		data.numReferenced++
	}()

	pfn := data.pageTable[pageNum].pfn

	if pfn < 0 { // page is currently not in a frame, put it there

		// frame is full, steal a page.
		if mmu.size == numFrames {

			// get the LRU entry from the tail
			tail := mmu.tail
			frameIndex = tail.my_page.pfn
			if debug {
				printMetadata()
				fmt.Printf("IN LRU WRITE, pageNum: %v\n, pfn: %v, tail page pfn: %v\n", pageNum, pfn, mmu.tail.my_page.pfn)
			}
			replace_pageNum := data.pageFrames[frameIndex] // virtual page number to be replaced
			if data.pageTable[replace_pageNum].dirty {     // if the page to be replaced is dirty, write it to swap
				data.pageTable[replace_pageNum].dirty = false
				data.pageTable[replace_pageNum].onDisk = true
				data.numWrittenToSwap++
			}
			data.pageTable[replace_pageNum].inSwap = true
			data.pageTable[replace_pageNum].pfn = -1
			data.numStolen++
			mmu.nodeTable[replace_pageNum] = nil

			// unlink tail and add new entry to front
			node = &LRUEntry{my_page: data.pageTable[pageNum]}
			if numFrames > 1 {
				old_head := mmu.head
				old_head.prev = node
				mmu.tail = tail.prev // assign new tail
				mmu.tail.nxt = node
				node.nxt = old_head
				node.prev = mmu.tail
				mmu.head = node // assign new head
			} else {
				node.nxt = node
				node.prev = node
				mmu.head = node
				mmu.tail = node
			}

		} else { // if mmu.size < 4, we still have space in the frame, use it instead of stealing pages
			frameIndex = mmu.size
			mmu.size++
			old_head := mmu.head
			if old_head == nil { // case frame is empty rn.
				node = &LRUEntry{my_page: data.pageTable[pageNum]}
				node.nxt = node
				node.prev = node
				mmu.head = node
				mmu.tail = node
			} else {
				node = &LRUEntry{my_page: data.pageTable[pageNum]}
				node.nxt = old_head
				node.prev = mmu.tail
				old_head.prev = node
				mmu.head = node // set new head to this new entry
				mmu.tail.nxt = node
			}
		}

		// update metadata
		if pfn == -2 { // page to write has never been mapped before
			data.numMapped++
		} else {
			if data.pageTable[pageNum].inSwap { // check if it's in swap
				if data.pageTable[pageNum].onDisk { // only true if this page has been written to in the past and was evicted after
					data.numRecoveredFromSwapped++
				}
				data.pageTable[pageNum].inSwap = false
			}
		}

		if isWrite {
			data.pageTable[pageNum].dirty = true
			data.pageTable[pageNum].onDisk = false
		}

		data.pageTable[pageNum].firstUsed = cnt
		data.pageTable[pageNum].pfn = frameIndex
		mmu.nodeTable[pageNum] = node
		data.pageFrames[frameIndex] = pageNum
		data.numMissed++

	} else { // node is already in frame, move it to the front
		if isWrite {
			data.pageTable[pageNum].dirty = true
		}
		node = mmu.nodeTable[pageNum]
		old_head := mmu.head

		if node == mmu.head {
			if debug {
				fmt.Println("ALR, node is HEAD")
			}
			return
		}
		if node == mmu.tail {
			mmu.tail = node.prev
			mmu.head = node
			node.nxt = old_head
			old_head.prev = node
			if debug {
				fmt.Println("ALR, node is TAIL")
			}
		} else {
			// unlink neighbors
			prev := node.prev
			nxt := node.nxt
			prev.nxt = nxt
			nxt.prev = prev

			// add to front
			node.nxt = old_head
			node.prev = old_head.prev
			old_head.prev = node
			mmu.head = node

			if debug {
				fmt.Println("ALR, node is between")
			}
		}
	}
}

// preprocess the page references (only possible because we know what pages will be used since the test cases are offline)
// create a linked list L[i] for each page i
// L[i] holds all the times that page i are referenced
func (mmu *OPTIMAL) Init() {
	mmu.refList = make([]*RefList, numPages)
	mmu.head = nil
	mmu.tail = nil
	cnt = 1
	for {
		inp, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
		}
		ss := strings.Split(inp, " ")
		ss[0] = strings.TrimRight(ss[0], "\n")
		if ss[0] == "#" {
			continue
		} else if ss[0] == "debug" {
			entry := &OpEntry{opType: 2}
			// update operation linked list tail
			if mmu.head == nil {
				mmu.head = entry
				mmu.tail = entry
			} else {
				mmu.tail.nxt = entry
				mmu.tail = entry
			}
		} else if ss[0] == "nodprintebug" {
			entry := &OpEntry{opType: 3}
			// update operation linked list tail
			if mmu.head == nil {
				mmu.head = entry
				mmu.tail = entry
			} else {
				mmu.tail.nxt = entry
				mmu.tail = entry
			}
		} else if ss[0] == "print" {
			entry := &OpEntry{opType: 4}
			// update operation linked list tail
			if mmu.head == nil {
				mmu.head = entry
				mmu.tail = entry
			} else {
				mmu.tail.nxt = entry
				mmu.tail = entry
			}
		} else { // parse input
			pageNum, offset, err := convertVirtualAddr(strings.TrimRight(ss[1], "\n"))
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return
			}
			if debug {
				fmt.Println(ss)
			}

			entry := &OpEntry{pageNum: pageNum, offset: offset}
			if ss[0] == "w" {
				entry.opType = 1
			}

			// update operation linked list tail
			if mmu.head == nil {
				mmu.head = entry
				mmu.tail = entry
			} else {
				mmu.tail.nxt = entry
				mmu.tail = entry
			}

			// update reference linked list for page pageNum
			refListEntry := &RefListEntry{timeRef: cnt, nxt: nil}
			if mmu.refList[pageNum] == nil {
				mmu.refList[pageNum] = &RefList{head: refListEntry, tail: refListEntry}
			} else {
				mmu.refList[pageNum].tail.nxt = refListEntry
				mmu.refList[pageNum].tail = refListEntry
			}
			cnt++
			//printMetadata()
		}
	}

	cnt = 1 // reset counter to for processing the operations in Access()
	entry := mmu.head
	for {
		if entry == nil {
			break
		}
		if entry.opType == 0 {
			mmu.Access(entry.pageNum, entry.offset, false)
			cnt++
		} else if entry.opType == 1 {
			mmu.Access(entry.pageNum, entry.offset, true)
			cnt++
		} else if entry.opType == 2 {
			debug = true
		} else if entry.opType == 3 {
			debug = false
		} else {
			printMetadata()
		}
		if debug {
			fmt.Println(entry)
			printMetadata()
		}
		entry = entry.nxt
	}
	printMetadata()
}

// newPageTime is the next use time of the page to be newly inserted into a frame
// this function sets maxFrameIndex to newPageFrameIndex if newPageTime is greater than maxFrameTime
// // if times are equal, set the one with the smaller index
// func (mmu *OPTIMAL) CheckReplaceFrameIndex(newPageTime int64, newPageFrameIndex int64) {
// 	if newPageTime > mmu.maxFrameTime {
// 		mmu.nxtMaxFrameIndex = mmu.maxFrameIndex
// 		mmu.nxtMaxFrameTime = mmu.maxFrameTime

// 		mmu.maxFrameIndex = newPageFrameIndex
// 		mmu.maxFrameTime = newPageTime

// 	} else if newPageTime == mmu.maxFrameTime {
// 		if newPageFrameIndex < mmu.maxFrameIndex {
// 			mmu.nxtMaxFrameIndex = mmu.maxFrameIndex
// 			mmu.nxtMaxFrameTime = mmu.maxFrameTime

// 			mmu.maxFrameIndex = newPageFrameIndex
// 			mmu.maxFrameTime = newPageTime
// 		}
// 	} else {
// 		if newPageTime > mmu.nxtMaxFrameTime {
// 			mmu.nxtMaxFrameIndex = newPageFrameIndex
// 			mmu.nxtMaxFrameTime = newPageTime

// 		} else if newPageTime == mmu.nxtMaxFrameTime {
// 			if newPageFrameIndex < mmu.nxtMaxFrameIndex {
// 				mmu.nxtMaxFrameIndex = newPageFrameIndex
// 				mmu.nxtMaxFrameTime = newPageTime
// 			}
// 		}
// 	}
// }

func (mmu *OPTIMAL) getReplaceFrameIndex() int64 {
	var max_time int64 = 0
	var max_index int64 = 0
	for i := int64(0); i < numFrames; i++ {
		pageNum := data.pageFrames[i]
		var cur_time int64

		if mmu.refList[pageNum].head == nil {
			cur_time = (1 << 63) - 1
		} else {
			cur_time = mmu.refList[pageNum].head.timeRef
		}

		if cur_time > max_time {
			max_time = cur_time
			max_index = i
		}
	}
	return max_index
}

func (mmu *OPTIMAL) Access(pageNum int64, offset int64, isWrite bool) {
	var frameIndex int64

	pfn := data.pageTable[pageNum].pfn
	if pfn < 0 { // page is currently not in a frame, put it there

		// frame is full, steal a page.
		if mmu.size == numFrames {
			//frameIndex = mmu.maxFrameIndex
			frameIndex = mmu.getReplaceFrameIndex()

			replace_pageNum := data.pageFrames[frameIndex] // virtual page number to be replaced
			if data.pageTable[replace_pageNum].dirty {     // if the page to be replaced is dirty, write it to swap
				data.pageTable[replace_pageNum].dirty = false
				data.pageTable[replace_pageNum].onDisk = true
				data.numWrittenToSwap++
			}
			data.pageTable[replace_pageNum].inSwap = true
			data.pageTable[replace_pageNum].pfn = -1
			data.numStolen++
		} else { // mmu.size < numFrames, we still have space in the frame, use it instead of stealing pages
			frameIndex = mmu.size
			mmu.size++
		}

		if pfn == -2 { // page to write has never been mapped before
			data.numMapped++
		} else {
			if data.pageTable[pageNum].inSwap { // check if it's in swap
				if data.pageTable[pageNum].onDisk { // only true if this page has been written to in the past
					data.numRecoveredFromSwapped++
				}
				data.pageTable[pageNum].inSwap = false
			}
		}

		data.pageTable[pageNum].pfn = frameIndex
		data.pageTable[pageNum].firstUsed = cnt
		if isWrite {
			data.pageTable[pageNum].dirty = true
			data.pageTable[pageNum].onDisk = false
		}

		data.pageFrames[frameIndex] = pageNum
		data.numMissed++

	} else { // page is currently in a frame
		if isWrite {
			data.pageTable[pageNum].dirty = true
		}
	}
	mmu.refList[pageNum].head = mmu.refList[pageNum].head.nxt // move the reference list of the current page by 1 to update the next use time
	// if mmu.refList[pageNum].head == nil {                     // this is the last time page pageNum will be referenced
	// 	mmu.CheckReplaceFrameIndex((1<<63)-1, frameIndex)
	// } else {
	// 	mmu.CheckReplaceFrameIndex(mmu.refList[pageNum].head.timeRef, frameIndex)
	// }
	data.pageTable[pageNum].lastUsed = cnt
	data.numReferenced++
}

// returns a pair of (page number, offset, error)
func convertVirtualAddr(_addr string) (int64, int64, error) {
	addr, err := strconv.ParseInt(_addr, 16, 64)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid hex input address: %v\n", _addr)
		os.Exit(-1)
	}
	pageNum := int64(addr / pageSize)

	if pageNum >= numPages {
		return -1, -1, errors.New(fmt.Sprintf("Input addrdata.pageTableess too large: %d\n", addr))
	}
	offset := addr % pageSize
	// if debug {
	// 	fmt.Fprintf(os.Stdout, "Converted addr %v -> %v -> %d, %d\n", _addr, addr, pageNum, offset)
	// }
	return pageNum, offset, nil
}

func parseFirstLine(res *int64, val string) {
	tmp, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid page size: %v\n", val)
		return
	}
	*res = tmp
}

func printMetadata() {
	var i int64

	fmt.Println("Page Table")
	for i = 0; i < numPages; i++ {
		var s strings.Builder

		// For test cases, we need to left-justify to 4 bytes...
		// WHY MUST WE LEFT-JUSTIFY THESE LINES TO 4 SPACES???
		// WHY NOT JUST LEFT-PAD WITH 4 SPACES INSTEAD??????
		tmp := fmt.Sprintf("%v", i)
		for j := 0; j <= 4-len(tmp); j++ {
			s.WriteString(" ")
		}
		s.WriteString(tmp)
		s.WriteString(" type:")
		if data.pageTable[i].pfn == -2 {
			s.WriteString("UNUSED")
		} else {
			if data.pageTable[i].pfn == -1 {
				s.WriteString("STOLEN")
				s.WriteString(" framenum:-1")
			} else {
				s.WriteString("MAPPED")
				s.WriteString(fmt.Sprintf(" framenum:%v", data.pageTable[i].pfn))
			}
			s.WriteString(" ondisk:")
			if data.pageTable[i].onDisk {
				s.WriteString("1")
			} else {
				s.WriteString("0")
			}
		}
		fmt.Printf("%v\n", s.String())
	}

	fmt.Println("Frame Table")
	for i = 0; i < numFrames; i++ {
		var s strings.Builder
		pageNum := data.pageFrames[i]
		tmp := fmt.Sprintf("%v", i)
		for j := 0; j <= 4-len(tmp); j++ {
			s.WriteString(" ")
		}
		s.WriteString(tmp)
		s.WriteString(" inuse:")
		if data.pageFrames[i] == -1 {
			s.WriteString("0")
		} else {
			s.WriteString("1")
			s.WriteString(" dirty:")
			if data.pageTable[pageNum].dirty {
				s.WriteString("1")
			} else {
				s.WriteString("0")
			}
			s.WriteString(" first_use:")
			s.WriteString(fmt.Sprintf("%v", data.pageTable[pageNum].firstUsed))
			s.WriteString(" last_use:")
			s.WriteString(fmt.Sprintf("%v", data.pageTable[pageNum].lastUsed))
		}

		fmt.Printf("%v\n", s.String())
	}

	fmt.Printf("Pages referenced: %v\n", data.numReferenced)
	fmt.Printf("Pages mapped: %v\n", data.numMapped)
	fmt.Printf("Page miss instances: %v\n", data.numMissed)
	fmt.Printf("Frame stolen instances: %v\n", data.numStolen)
	fmt.Printf("Stolen frames written to swapspace: %v\n", data.numWrittenToSwap)
	fmt.Printf("Stolen frames recovered from swapspace: %v\n", data.numRecoveredFromSwapped)
}

var mmu MMU

func main() {
	argsWithoutProg := os.Args[1:]
	if argsWithoutProg[0] == "-w" {
		useBackingBlocks = true
		argsWithoutProg = argsWithoutProg[1:]
	}
	file, ok := os.Open(argsWithoutProg[1])
	if ok != nil {
		fmt.Fprintln(os.Stderr, "Failed to open input file...")
		return
	}

	reader = bufio.NewReader(file)
	for {
		inp, err := reader.ReadString('\n')
		if err != nil {
			fmt.Fprintf(os.Stderr, "Invalid input format")
			return
		}
		if inp[0] != '#' {
			nums := strings.Split(inp, " ")
			if len(nums) != 4 {
				fmt.Fprintf(os.Stderr, "Invalid input format")
				return
			}
			parseFirstLine(&pageSize, nums[0])
			parseFirstLine(&numFrames, nums[1])
			parseFirstLine(&numPages, nums[2])
			parseFirstLine(&numBackingBlocks, strings.TrimRight(nums[3], "\n"))
			break
		}
	}
	fmt.Printf("Page size: %v\n", pageSize)
	fmt.Printf("Num frames: %v\n", numFrames)
	fmt.Printf("Num pages: %v\n", numPages)
	fmt.Printf("Num backing blocks: %v\n", numBackingBlocks)

	pageTable := make([]*page, numPages)
	var i int64
	for i = 0; i < numPages; i++ {
		pageTable[i] = &page{pfn: -2, dirty: false, inSwap: false, onDisk: false}
	}
	pageFrames := make([]int64, numFrames)
	for i = 0; i < numFrames; i++ {
		pageFrames[i] = -1
	}
	data = page_metadata{pageTable: pageTable, pageFrames: pageFrames}

	if argsWithoutProg[0] == "FIFO" {
		mmu = &FIFO{nxt_idx: make(chan int64, numFrames)}
		fmt.Printf("Reclaim algorithm: FIFO\n")
	} else if argsWithoutProg[0] == "LRU" {
		mmu = &LRU{head: nil, tail: nil, size: 0}
		fmt.Printf("Reclaim algorithm: LRU\n")
	} else {
		mmu = &OPTIMAL{}
		fmt.Printf("Reclaim algorithm: OPTIMAL\n")
		mmu.Init() // will read in the page references, do the preprocessing for OPT, then process the references
		return
	}
	mmu.Init()

	for {
		inp, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
		}
		ss := strings.Split(inp, " ")
		ss[0] = strings.TrimRight(ss[0], "\n")
		if ss[0] == "#" {
			continue
		} else if ss[0] == "debug" {
			debug = true
		} else if ss[0] == "nodprintebug" {
			debug = false
		} else if ss[0] == "print" {
			printMetadata()
		} else { // parse input
			pageNum, offset, err := convertVirtualAddr(strings.TrimRight(ss[1], "\n"))
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return
			}
			if debug {
				fmt.Println(ss)
			}
			if ss[0] == "w" {
				mmu.Access(pageNum, offset, true)
			} else {
				mmu.Access(pageNum, offset, false)
			}
			cnt++
			//printMetadata()
		}
	}
	printMetadata()
}
