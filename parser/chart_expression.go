package parser

func (c *chart[N]) expressionStart(cell *chartCell, rule site, probe int) {
	for probe < len(rule.operators) && rule.operators[probe].Form != Prefix {
		probe++
	}
	if probe == len(rule.operators) {
		child := c.follow(cell, childKey(cell, rule.children[0], cell.key.point), func(value chartResult) {
			if value.end == cell.key.point && value.cost == 0 {
				c.err = grammarError("expression atom succeeded without consuming a token")
				return
			}
			c.expressionContinue(cell, rule, value, -1, 0)
		})
		c.onMiss(cell, child, func(miss chartMiss) chartMiss {
			return chartExpressionMiss(cell.entry(), miss)
		})
		return
	}
	op := rule.operators[probe]
	request := operatorRequest(cell, op)
	c.offer(cell, cell.key.point, request, func(read Read) {
		if !Matches(c.parser.look(read), op.Match) {
			c.enqueue(func() { c.expressionStart(cell, rule, probe+1) })
			return
		}
		entry := cell.entry()
		token := chartTaken(entry, read)
		c.expressionOperand(cell, rule, entry, op, token, -1)
	})
}

func operatorRequest(cell *chartCell, op expressionOperator) Request {
	if op.Goal != nil {
		return *op.Goal
	}
	return cell.key.request
}

func (c *chart[N]) expressionContinue(cell *chartCell, rule site, left chartResult, barred, probe int) {
	for probe < len(rule.operators) {
		op := rule.operators[probe]
		if op.Form != Prefix && cell.key.power.allows(op.Power) {
			break
		}
		probe++
	}
	if probe == len(rule.operators) {
		c.emit(cell, left)
		return
	}
	op := rule.operators[probe]
	request := operatorRequest(cell, op)
	c.offer(cell, left.end, request, func(read Read) {
		look := c.parser.look(read)
		if !Matches(look, op.Match) {
			c.enqueue(func() { c.expressionContinue(cell, rule, left, barred, probe+1) })
			return
		}
		if op.Form == Infix && op.Power == barred {
			left.out = nil
			c.reject(cell, &chartMiss{chartResult: left, found: look, request: request, commit: true})
			return
		}
		token := chartTaken(left, read)
		if op.Form != Postfix || op.tailChild != 0 {
			c.expressionOperand(cell, rule, left, op, token, barred)
			return
		}
		c.reduceExpression(cell, rule, left, op, token, chartResult{end: token.end}, barred, chartAppend(left, token))
	})
}

func (c *chart[N]) expressionOperand(cell *chartCell, rule site, left chartResult, op expressionOperator, token chartResult, barred int) {
	var key chartKey
	if op.Form == Postfix {
		key = resumeKey(cell, rule.children[op.tailChild], token)
	} else {
		key = resumeKey(cell, cell.key.site, token)
		key.power = bindingPower{minimum: op.Power, strict: op.Form == Infix && op.Assoc != Right}
	}
	child := c.follow(cell, key, func(right chartResult) {
		c.extend(cell, chartAppend(left, token), right, func(result chartResult) {
			c.reduceExpression(cell, rule, left, op, token, right, barred, result)
		})
	})
	c.onMiss(cell, child, func(miss chartMiss) chartMiss {
		return chartExpressionMiss(chartAppend(left, token), miss)
	})
}

func chartExpressionMiss(prefix chartResult, miss chartMiss) chartMiss {
	order := chartOrder(prefix.order, miss.order, prefix.end.mark)
	miss.chartResult = chartAppend(prefix, miss.chartResult)
	miss.order, miss.out = order, nil
	miss.commit = miss.commit || miss.takes != 0 || miss.cost != 0
	return miss
}

func (c *chart[N]) reduceExpression(cell *chartCell, rule site, left chartResult, op expressionOperator, token, right chartResult, barred int, result chartResult) {
	operator := chartCapture("operator", token)
	var fields *chartOutput
	switch op.Form {
	case Prefix:
		fields = chartJoin(operator, chartCapture("operand", right))
	case Infix:
		fields = chartJoin(chartJoin(chartCapture("left", left), operator), chartCapture("right", right))
		barred = -1
		if op.Assoc == NonAssoc {
			barred = op.Power
		}
	case Postfix:
		fields = chartJoin(chartCapture("left", left), operator)
		if op.tailChild != 0 {
			fields = chartJoin(fields, chartCapture("suffix", right))
		}
	}
	node := &chartNode{
		slot: rule.name, kind: op.Kind, span: c.span(cell.key.point, result.end), body: fields, hasError: result.cost != 0,
	}
	result.out = chartNodeOutput(node, result.tape)
	c.enqueue(func() { c.expressionContinue(cell, rule, result, barred, 0) })
}
