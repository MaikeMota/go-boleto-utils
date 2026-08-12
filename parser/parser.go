package parser

import (
	"errors"
	"fmt"
	"github.com/fonini/go-boleto-utils/utils"
	"strconv"
	"time"
)

const (
	DigitableLine utils.BoletoCodeType = "DIGITABLE_LINE"
	Barcode       utils.BoletoCodeType = "BARCODE"
	Unknown       utils.BoletoCodeType = "UNKNOWN"

	BaseDateFormat = "2006-01-02 15:04:05"
)

// Parse parses a digitable line or a barcode into a Boleto struct.
// Routes to the bank-boleto parser or the arrecadacao/convenio parser
// depending on GetBoletoType — the two families use unrelated field
// layouts (see parseDigitableLine vs parseArrecadacaoBarcode) and mixing
// them up silently produces plausible-looking but wrong data.
func Parse(code string) (*utils.Boleto, error) {
	line := utils.OnlyNumbers(code)

	codeType, err := GetCodeType(line)
	if err != nil {
		return nil, err
	}

	boletoType := GetBoletoType(line)

	var boleto *utils.Boleto
	if boletoType == utils.Bank || boletoType == utils.CreditCard {
		bankLine := line
		if codeType == Barcode {
			bankLine = ConvertBarcodeToDigitableLine(bankLine)
		}
		boleto, err = parseDigitableLine(bankLine)
	} else {
		barcode44 := line
		if len(line) == 48 {
			barcode44 = convertArrecadacaoDigitableLineToBarcode(line)
		}
		boleto, err = parseArrecadacaoBarcode(barcode44)
	}
	if err != nil {
		return nil, err
	}

	boleto.Type = boletoType
	boleto.CodeType = codeType

	return boleto, nil
}

func GetCodeType(code string) (utils.BoletoCodeType, error) {
	code = utils.OnlyNumbers(code)

	switch len(code) {
	case 44:
		return Barcode, nil
	case 46, 47, 48:
		return DigitableLine, nil
	default:
		return Unknown, errors.New("unknown code")
	}
}

func GetBoletoType(code string) utils.BoletoType {
	code = utils.OnlyNumbers(code)

	if code[len(code)-14:] == "00000000000000" || utils.Substr(code, 5, 14) == "00000000000000" {
		return utils.CreditCard
	} else if utils.Substr(code, 0, 1) == "8" {
		digit := utils.Substr(code, 1, 1)

		switch digit {
		case "1":
			return utils.CityHalls
		case "2":
			return utils.Sanitation
		case "3":
			return utils.ElectricityAndGas
		case "4":
			return utils.Telecommunications
		case "5":
			return utils.GovernmentAgencies
		case "6", "9":
			return utils.PaymentBooklets
		case "7":
			return utils.TrafficFines
		}
	}

	return utils.Bank
}

func parseDigitableLine(line string) (*utils.Boleto, error) {
	var boleto utils.Boleto

	boleto.IssuerBankCode = utils.Substr(line, 0, 3)
	boleto.IssuerBankName = utils.Banks[boleto.IssuerBankCode]

	boleto.Currency, _ = strconv.Atoi(utils.Substr(line, 3, 1))

	boleto.IssuerReserved1 = utils.Substr(line, 4, 5)
	boleto.CheckDigit1, _ = strconv.Atoi(utils.Substr(line, 9, 1))

	boleto.IssuerReserved2 = utils.Substr(line, 10, 10)
	boleto.CheckDigit2, _ = strconv.Atoi(utils.Substr(line, 20, 1))

	boleto.IssuerReserved3 = utils.Substr(line, 21, 10)
	boleto.CheckDigit3, _ = strconv.Atoi(utils.Substr(line, 31, 1))

	boleto.GeneralCheckDigit, _ = strconv.Atoi(utils.Substr(line, 32, 1))

	dueDate, err := calculateDueDate(utils.Substr(line, 33, 4))
	if err != nil {
		return nil, err
	}
	boleto.DueDate = dueDate

	amount, err := parseAmount(utils.Substr(line, 37, 10))
	if err != nil {
		return nil, err
	}
	boleto.Amount = amount

	return &boleto, nil
}

// calculateDueDate converts the 4-digit "fator de vencimento" into a date.
//
// FEBRABAN communication FB-009/2023: the factor is days elapsed since a
// base date, and the field is only 4 digits wide (max 9999). That ceiling
// was reached on 2025-02-21, so from 2025-02-22 onward the factor restarts
// at 1000 against a NEW base date (2025-02-22) instead of continuing to
// count from the original 1997-10-07 base.
//
// This makes any factor >= 1000 genuinely ambiguous — the legacy rule was
// never restricted to factors under 1000, so e.g. factor 9771 legitimately
// meant 2024-07-08 under the old rule (confirmed against this library's own
// pre-existing test fixtures) just as validly as it could mean 2049-02-27
// under the new one. There is no way to tell which rule produced a given
// factor from the digits alone.
//
// The two candidate dates for the same factor are always ~27 years apart
// (9999 days) and fall on opposite sides of the 2025-02-22 cutover, so
// picking whichever candidate is closer to "now" resolves the ambiguity
// correctly for any boleto whose due date isn't wildly far from when it's
// actually being parsed — true both for freshly-issued boletos (which are
// always close to the real "now") and, empirically, for this library's own
// historical test fixtures (2018-2024 dates, still far closer to a 2026
// "now" than their new-rule alternates would be). This assumption only
// breaks down decades from now, near the eventual next overflow — same
// category of temporal assumption the factor scheme itself already makes.
const (
	NewBaseDate       = "2025-02-22 00:00:00"
	NewBaseDateOffset = 1000
)

func calculateDueDate(dueDateStr string) (time.Time, error) {
	factor, err := strconv.Atoi(dueDateStr)
	if err != nil {
		return time.Time{}, err
	}

	legacyBase, err := time.Parse(BaseDateFormat, utils.BaseDate)
	if err != nil {
		return time.Time{}, err
	}
	legacyDate := legacyBase.AddDate(0, 0, factor)

	if factor < NewBaseDateOffset {
		// Structurally impossible under the new rule (which starts at
		// 1000) — unambiguously legacy, including the factor=0 sentinel.
		return legacyDate, nil
	}

	newBase, err := time.Parse(BaseDateFormat, NewBaseDate)
	if err != nil {
		return time.Time{}, err
	}
	newDate := newBase.AddDate(0, 0, factor-NewBaseDateOffset)

	now := time.Now()
	if abs(now.Sub(newDate)) < abs(now.Sub(legacyDate)) {
		return newDate, nil
	}
	return legacyDate, nil
}

func abs(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

func parseAmount(amountStr string) (float64, error) {
	amount, err := strconv.ParseFloat(amountStr, 64)
	if err != nil {
		return 0, err
	}
	return amount / 100, nil
}

func ConvertBarcodeToDigitableLine(barcode string) string {
	block1 := barcode[0:4] + barcode[19:24]
	cd1 := utils.CalculateVerificationDigit(block1)

	block2 := barcode[24:34]
	cd2 := utils.CalculateVerificationDigit(block2)

	block3 := barcode[34:44]
	cd3 := utils.CalculateVerificationDigit(block3)

	return fmt.Sprintf(
		"%s%s%s%s%s%s%s%s%s%s%s",
		block1[:5], block1[5:], cd1,
		block2[:5], block2[5:], cd2,
		block3[:5], block3[5:], cd3,
		barcode[4:5], barcode[5:19],
	)
}
