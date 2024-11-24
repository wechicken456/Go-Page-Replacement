CC=gcc
CXX=g++
CFLAGS=-Wall -Werror -O2
CXXFLAGS=${CFLAGS}

# this rule MUST create an executable program/script called vm (which can be called as "./vm") in the current directory so that it can be tested
default: vm

# this is how you might make the program if written in C/C++
# alter this as necessary for your program
# If you're using python or other non-compiled language, see python_wrapper
#vm: vm.o future.o 
#vm.o: vm.h
#future.o: vm.h
vm: main.go
	go build
	mv ./main ./vm

clean: 
	rm -f *.o vm .test.results
	rm -rf results

.PHONY: test
test: vm
	chmod +rx test testoptimal testbs
	rm -f .test.results
	-./test input.0.psize4k 
	-./test input.0.psize5
	-./test input.0.psize10
	-./test input.0.psize1
	-./test input.1.easy
	-./test input.1.eachstep
	-./test input.1.lru
	-./test input.2.only1frame
	-./test input.handout
	-./test input.b.p440
	-./test input.b.p442
	-./test input.b.p443
	-./test input.b.belady1
	-./test input.b.belady2
	-./test input.o.optimal
	-./test input.w.bs
	-./test input.w.disk
	-./test input.w.ondisk_test
	-./test input.9.bigrandom
	-./testoptimal
	-./testbs
	-echo "Test results: "; cat .test.results
