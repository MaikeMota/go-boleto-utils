package parser

import (
	"errors"
	"strconv"
	"time"

	"github.com/fonini/go-boleto-utils/utils"
)

// Arrecadacao/convenio boletos (utility bills, city hall taxes, telecom,
// etc.) use a completely different 44-digit barcode layout than bank
// collection boletos — FEBRABAN "Layout Padrao de Arrecadacao/Recebimento
// com Utilizacao do Codigo de Barras", version 7 (01/03/2023):
//
//	Position  Size  Content
//	1          1    Product identifier — constant "8"
//	2          1    Segment (city halls, sanitation, electricity/gas, ...)
//	3          1    Value-type: 6/8 = actual value in reais, 7/9 = reference
//	                quantity/index-adjusted value. 6/7 use a mod10 general
//	                check digit, 8/9 use mod11 (not validated here — this
//	                package only decodes, matching how the bank-boleto
//	                parser doesn't validate check digits either).
//	4          1    General check digit
//	5-15      11    Amount (integer, /100 for reais) — always present
//	16-19      4    Company/org ID (or 16-23, 8 digits, if CNPJ-identified)
//	20-44     25    Free field, company-defined. If a due date is encoded,
//	                it is always the first 8 digits, format YYYYMMDD — but
//	                this is optional per the spec, so a missing/unparsable
//	                due date here is expected for many issuers, not an error.

const arrecadacaoBarcodeLength = 44

// convertArrecadacaoDigitableLineToBarcode strips the per-block check
// digit from a 48-digit arrecadacao linha digitavel (4 blocks of 11 data
// digits + 1 check digit each) to recover the 44-digit barcode. Unlike the
// bank-boleto linha digitavel, this is a straight positional trim — no
// block reassembly needed.
func convertArrecadacaoDigitableLineToBarcode(line string) string {
	if len(line) != 48 {
		return line
	}
	return line[0:11] + line[12:23] + line[24:35] + line[36:47]
}

func parseArrecadacaoBarcode(barcode string) (*utils.Boleto, error) {
	if len(barcode) != arrecadacaoBarcodeLength {
		return nil, errors.New("invalid arrecadacao barcode length")
	}

	var boleto utils.Boleto

	boleto.GeneralCheckDigit, _ = strconv.Atoi(utils.Substr(barcode, 3, 1))

	amount, err := parseAmount(utils.Substr(barcode, 4, 11))
	if err != nil {
		return nil, err
	}
	boleto.Amount = amount

	freeField := utils.Substr(barcode, 19, 25)
	if dueDate, ok := parseArrecadacaoDueDate(freeField); ok {
		boleto.DueDate = dueDate
	}

	return &boleto, nil
}

// parseArrecadacaoDueDate reads the optional due date from the first 8
// digits of the free field (YYYYMMDD). Per FEBRABAN spec this field is
// entirely company-defined and a due date is not guaranteed to be there at
// all, so a missing or implausible date is a normal outcome, not an error —
// callers should fall back to another source (e.g. text extraction) when
// ok is false.
func parseArrecadacaoDueDate(freeField string) (time.Time, bool) {
	if len(freeField) < 8 {
		return time.Time{}, false
	}
	t, err := time.Parse("20060102", freeField[:8])
	if err != nil {
		return time.Time{}, false
	}
	if t.Year() < 2000 || t.Year() > 2099 {
		return time.Time{}, false
	}
	return t, true
}
