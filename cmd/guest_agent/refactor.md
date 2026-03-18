I'll analyze the codebase to identify refactoring opportunities following Go best practices and create a plan to improve code organization and reduce file sizes.







# 📋 TrustForge Refactoring Plan

## 🎯 Current Issues Identified

### **Large Files (>300 lines):**
- [cmd/guest_agent/main.go](cci:7://file:///Users/nutcase/Downloads/trustforge/cmd/guest_agent/main.go:0:0-0:0) (513 lines) - **CRITICAL**
- `internal/worker/pool.go` (320 lines) - **MEDIUM**  
- `internal/db/repository.go` (319 lines) - **MEDIUM**
- [internal/vm/factory.go](cci:7://file:///Users/nutcase/Downloads/trustforge/internal/vm/factory.go:0:0-0:0) (307 lines) - **MEDIUM**
- [test/integration/pipeline_test.go](cci:7://file:///Users/nutcase/Downloads/trustforge/test/integration/pipeline_test.go:0:0-0:0) (320 lines) - **LOW**

### **Code Organization Issues:**
- Mixed responsibilities in single files
- Large functions with multiple concerns
- Repeated patterns and constants
- Missing interfaces for testing

## 🚀 Refactoring Strategy

## **Phase 1: Guest Agent Refactoring (HIGH PRIORITY)**

### **Current Problems:**
- 513 lines in single file
- Mixed: vsock handling, command processing, execution, resource management
- Hard to test and maintain

### **Proposed Structure:**
```
cmd/guest_agent/
├── main.go                 (~50 lines) - Entry point only
├── internal/
│   ├── vsock/
│   │   ├── client.go       (~80 lines) - vsock connection handling
│   │   ├── transport.go    (~60 lines) - low-level vsock ops
│   │   └── types.go         (~20 lines) - vsock types/constants
│   ├── executor/
│   │   ├── verifier.go     (~100 lines) - verifier execution logic
│   │   ├── limits.go       (~60 lines) - resource limit management
│   │   └── result.go       (~40 lines) - result processing
│   ├── server/
│   │   ├── handler.go      (~80 lines) - command handling
│   │   ├── ratelimit.go    (~40 lines) - rate limiting
│   │   └── validation.go   (~30 lines) - input validation
│   └── poweroff.go         (~20 lines) - system shutdown
```

## **Phase 2: Worker Pool Refactoring (MEDIUM PRIORITY)**

### **Current Problems:**
- 320 lines with mixed concerns
- Job processing, pool management, metrics in one file

### **Proposed Structure:**
```
internal/worker/
├── pool.go                 (~80 lines) - Core pool interface
├── job.go                  (~40 lines) - Job types and constants  
├── processor.go            (~100 lines) - Job processing logic
├── metrics.go              (~60 lines) - Worker metrics
└── scheduler.go            (~40 lines) - Job scheduling
```

## **Phase 3: VM Factory Refactoring (MEDIUM PRIORITY)**

### **Current Problems:**
- 307 lines with snapshot management, config building, lifecycle
- Missing interfaces for testing

### **Proposed Structure:**
```
internal/vm/
├── factory.go              (~60 lines) - Factory interface and main logic
├── snapshot.go             (~100 lines) - Snapshot management
├── config.go               (~80 lines) - VM configuration builders
├── lifecycle.go            (~40 lines) - VM lifecycle management
└── types.go                (~30 lines) - VM types and constants
```

## **Phase 4: Database Refactoring (MEDIUM PRIORITY)**

### **Current Problems:**
- 319 lines with mixed query types
- No query separation by concern

### **Proposed Structure:**
```
internal/db/
├── repository.go           (~60 lines) - Repository interface
├── submissions.go          (~100 lines) - Submission queries
├── snapshots.go            (~80 lines) - Snapshot management queries
├── migrations.go           (~40 lines) - Database migrations
└── pool.go                 (~40 lines) - Connection pool management
```

## **Phase 5: General Improvements (LOW PRIORITY)**

### **Interfaces and Testing:**
- Add interfaces for all major components
- Create mock implementations for testing
- Extract constants to dedicated files

### **Shared Packages:**
```
pkg/
├── constants/              (~50 lines) - Application-wide constants
├── errors/                 (~40 lines) - Custom error types
└── validation/             (~60 lines) - Input validation utilities
```

## 📊 Expected Benefits

### **Code Quality:**
- **Single Responsibility**: Each file has one clear purpose
- **Testability**: Smaller, focused functions are easier to test
- **Maintainability**: Changes are localized and predictable

### **File Size Reduction:**
- [cmd/guest_agent/main.go](cci:7://file:///Users/nutcase/Downloads/trustforge/cmd/guest_agent/main.go:0:0-0:0): 513 → 50 lines (90% reduction)
- `internal/worker/pool.go`: 320 → 80 lines (75% reduction)
- [internal/vm/factory.go](cci:7://file:///Users/nutcase/Downloads/trustforge/internal/vm/factory.go:0:0-0:0): 307 → 60 lines (80% reduction)
- `internal/db/repository.go`: 319 → 60 lines (81% reduction)

### **Go Best Practices:**
- **Package by feature**, not by layer
- **Interface-driven design** for testability
- **Explicit error handling** with custom types
- **Dependency injection** through constructors
- **Constants in dedicated packages**

## 🛠 Implementation Order

1. **Phase 1**: Guest agent (biggest impact)
2. **Phase 2**: Worker pool (medium impact)  
3. **Phase 3**: VM factory (medium impact)
4. **Phase 4**: Database (medium impact)
5. **Phase 5**: Shared utilities (low impact)

## 🎯 Success Metrics

- All files under 200 lines
- Each package has <5 files
- 100% test coverage for business logic
- Zero circular dependencies
- Clear separation of concerns

Would you like me to start with **Phase 1: Guest Agent Refactoring** since it has the biggest impact?