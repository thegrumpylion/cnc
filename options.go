package cli

// ParserOption option type for Parser
type ParserOption func(p *Parser)

// WithArgCase set the arg case. default is CaseCamelLower
func WithArgCase(c Case) ParserOption {
	return func(p *Parser) {
		p.argCase = c
	}
}

// WithEnvCase set the env case. default is CaseSnakeUpper
func WithEnvCase(c Case) ParserOption {
	return func(p *Parser) {
		p.envCase = c
	}
}

// WithCmdCase set the cmd case. default is CaseLower
func WithCmdCase(c Case) ParserOption {
	return func(p *Parser) {
		p.cmdCase = c
	}
}

// WithArgSplicer set the arg splicer
func WithArgSplicer(s Splicer) ParserOption {
	return func(p *Parser) {
		p.argSplicer = s
	}
}

// WithEnvSplicer set the env splicer
func WithEnvSplicer(s Splicer) ParserOption {
	return func(p *Parser) {
		p.envSplicer = s
	}
}

// WithOnErrorStrategy sets the execution strategy for handling errors
func WithOnErrorStrategy(str OnErrorStrategy) ParserOption {
	return func(p *Parser) {
		p.strategy = str
	}
}

// WithGlobalArgsEnabled enable global argumets
func WithGlobalArgsEnabled() ParserOption {
	return func(p *Parser) {
		p.globalsEnabled = true
	}
}