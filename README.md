# Go-Page-Replacement
Page replacement algorithms FIFO, LRU, and OPTIMAL implemented in Golang. All implementations are of my own. 

This is one of the assignments from CS4420 - Operating Systems Fall 2024, Ohio University, taught by Dr. Ostermann. 

## FIFO
Use a channel to simulate a queue.

## LRU
Traded an extra `O(n)` space for `O(1)` search & update time complexity, where `n` is the number of pages.

Have a linked list of LRU entries. Each page steal will pop the tail and add a new entry for the newly added page as the new head.

Accessing a page in the linked list will just unlink it from its neighbors, connect its neighbors together, then make it the new head of the LRU linked list.

## OPTIMAL
For each page, store a linked list of its reference times, in ascending order.

At all points in the program, the head of the linked list for a page will tell us its closet next access/reference time. 

Each time we need to steal a page, loop through all frames, check the head of each page at each frame for its next reference time, get the max, remove that page, then add the new page.


## Backing Store
Bascially is like another page table. The *first time* a page gets written into swap space (when it was dirty then stolen), it will be put into a block/index within the backing store table.

Every time a **dirty** page with a backing block is stolen, we increment the count of *write* of that backing block.

Every time a page with a backing block is **read**, we increment the count of *read* of that backing block.

Of course, this ONLY works if the number of dirty first-time-stolen pages <= number of backing blocks. And the test cases provided gurantee this.