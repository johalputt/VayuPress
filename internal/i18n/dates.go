// SPDX-License-Identifier: Apache-2.0

package i18n

// dates.go — dates as each language writes them, for the pages a visitor
// reads (a booking page's month, its days and times). Month and weekday
// names and the order of the parts are CLDR's (Unicode CLDR, under the
// Unicode licence), copied as data: golang.org/x/text has no date
// formatting. Times are the 24-hour clock in every language here, as the
// pages were in English.

import (
	"strconv"
	"strings"
	"time"
)

// DateForm is how much of a date is written.
type DateForm int

// The forms the pages use.
const (
	MonthYear      DateForm = iota // January 2026
	DayMonth                       // Monday 2 January
	Day                            // Monday 2
	DayMonthAt                     // Monday 2 January, 15:04
	DayMonthYearAt                 // Monday 2 January 2026, 15:04
)

type dateNames struct {
	months      [12]string // in a date ("2 января")
	monthsAlone [12]string // standing alone ("январь 2026")
	days        [7]string  // Sunday first, as time.Weekday counts
	short       [7]string  // the calendar's column heads, Monday first
	// The patterns, with {wd} {d} {m} {y} {t} for the weekday, day, month,
	// year and time, in the order of the forms.
	monthYear, dayMonth, day, dayMonthAt, dayMonthYearAt string
}

var dates = map[string]dateNames{
	"en": {
		[12]string{"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"},
		[12]string{"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"},
		[7]string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"},
		[7]string{"Mo", "Tu", "We", "Th", "Fr", "Sa", "Su"},
		"{m} {y}", "{wd} {d} {m}", "{wd} {d}", "{wd} {d} {m}, {t}", "{wd} {d} {m} {y}, {t}"},
	"es": {
		[12]string{"enero", "febrero", "marzo", "abril", "mayo", "junio", "julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre"},
		[12]string{"enero", "febrero", "marzo", "abril", "mayo", "junio", "julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre"},
		[7]string{"domingo", "lunes", "martes", "miércoles", "jueves", "viernes", "sábado"},
		[7]string{"L", "M", "X", "J", "V", "S", "D"},
		"{m} de {y}", "{wd}, {d} de {m}", "{wd} {d}", "{wd}, {d} de {m}, {t}", "{wd}, {d} de {m} de {y}, {t}"},
	"pt-BR": {
		[12]string{"janeiro", "fevereiro", "março", "abril", "maio", "junho", "julho", "agosto", "setembro", "outubro", "novembro", "dezembro"},
		[12]string{"janeiro", "fevereiro", "março", "abril", "maio", "junho", "julho", "agosto", "setembro", "outubro", "novembro", "dezembro"},
		[7]string{"domingo", "segunda-feira", "terça-feira", "quarta-feira", "quinta-feira", "sexta-feira", "sábado"},
		[7]string{"S", "T", "Q", "Q", "S", "S", "D"},
		"{m} de {y}", "{wd}, {d} de {m}", "{wd}, {d}", "{wd}, {d} de {m}, {t}", "{wd}, {d} de {m} de {y}, {t}"},
	"fr": {
		[12]string{"janvier", "février", "mars", "avril", "mai", "juin", "juillet", "août", "septembre", "octobre", "novembre", "décembre"},
		[12]string{"janvier", "février", "mars", "avril", "mai", "juin", "juillet", "août", "septembre", "octobre", "novembre", "décembre"},
		[7]string{"dimanche", "lundi", "mardi", "mercredi", "jeudi", "vendredi", "samedi"},
		[7]string{"L", "M", "M", "J", "V", "S", "D"},
		"{m} {y}", "{wd} {d} {m}", "{wd} {d}", "{wd} {d} {m}, {t}", "{wd} {d} {m} {y}, {t}"},
	"de": {
		[12]string{"Januar", "Februar", "März", "April", "Mai", "Juni", "Juli", "August", "September", "Oktober", "November", "Dezember"},
		[12]string{"Januar", "Februar", "März", "April", "Mai", "Juni", "Juli", "August", "September", "Oktober", "November", "Dezember"},
		[7]string{"Sonntag", "Montag", "Dienstag", "Mittwoch", "Donnerstag", "Freitag", "Samstag"},
		[7]string{"Mo", "Di", "Mi", "Do", "Fr", "Sa", "So"},
		"{m} {y}", "{wd}, {d}. {m}", "{wd}, {d}.", "{wd}, {d}. {m}, {t}", "{wd}, {d}. {m} {y}, {t}"},
	"it": {
		[12]string{"gennaio", "febbraio", "marzo", "aprile", "maggio", "giugno", "luglio", "agosto", "settembre", "ottobre", "novembre", "dicembre"},
		[12]string{"gennaio", "febbraio", "marzo", "aprile", "maggio", "giugno", "luglio", "agosto", "settembre", "ottobre", "novembre", "dicembre"},
		[7]string{"domenica", "lunedì", "martedì", "mercoledì", "giovedì", "venerdì", "sabato"},
		[7]string{"L", "M", "M", "G", "V", "S", "D"},
		"{m} {y}", "{wd} {d} {m}", "{wd} {d}", "{wd} {d} {m}, {t}", "{wd} {d} {m} {y}, {t}"},
	"ru": {
		[12]string{"января", "февраля", "марта", "апреля", "мая", "июня", "июля", "августа", "сентября", "октября", "ноября", "декабря"},
		[12]string{"январь", "февраль", "март", "апрель", "май", "июнь", "июль", "август", "сентябрь", "октябрь", "ноябрь", "декабрь"},
		[7]string{"воскресенье", "понедельник", "вторник", "среда", "четверг", "пятница", "суббота"},
		[7]string{"Пн", "Вт", "Ср", "Чт", "Пт", "Сб", "Вс"},
		"{m} {y}", "{wd}, {d} {m}", "{wd}, {d}", "{wd}, {d} {m}, {t}", "{wd}, {d} {m} {y} г., {t}"},
	"tr": {
		[12]string{"Ocak", "Şubat", "Mart", "Nisan", "Mayıs", "Haziran", "Temmuz", "Ağustos", "Eylül", "Ekim", "Kasım", "Aralık"},
		[12]string{"Ocak", "Şubat", "Mart", "Nisan", "Mayıs", "Haziran", "Temmuz", "Ağustos", "Eylül", "Ekim", "Kasım", "Aralık"},
		[7]string{"Pazar", "Pazartesi", "Salı", "Çarşamba", "Perşembe", "Cuma", "Cumartesi"},
		[7]string{"Pt", "Sa", "Ça", "Pe", "Cu", "Ct", "Pz"},
		"{m} {y}", "{d} {m} {wd}", "{d} {wd}", "{d} {m} {wd}, {t}", "{d} {m} {y} {wd}, {t}"},
	"zh-Hans": {
		[12]string{"1月", "2月", "3月", "4月", "5月", "6月", "7月", "8月", "9月", "10月", "11月", "12月"},
		[12]string{"1月", "2月", "3月", "4月", "5月", "6月", "7月", "8月", "9月", "10月", "11月", "12月"},
		[7]string{"星期日", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六"},
		[7]string{"一", "二", "三", "四", "五", "六", "日"},
		"{y}年{m}", "{m}{d}日 {wd}", "{d}日 {wd}", "{m}{d}日 {wd} {t}", "{y}年{m}{d}日 {wd} {t}"},
	"ja": {
		[12]string{"1月", "2月", "3月", "4月", "5月", "6月", "7月", "8月", "9月", "10月", "11月", "12月"},
		[12]string{"1月", "2月", "3月", "4月", "5月", "6月", "7月", "8月", "9月", "10月", "11月", "12月"},
		[7]string{"日曜日", "月曜日", "火曜日", "水曜日", "木曜日", "金曜日", "土曜日"},
		[7]string{"月", "火", "水", "木", "金", "土", "日"},
		"{y}年{m}", "{m}{d}日 {wd}", "{d}日 {wd}", "{m}{d}日 {wd} {t}", "{y}年{m}{d}日 {wd} {t}"},
	"ko": {
		[12]string{"1월", "2월", "3월", "4월", "5월", "6월", "7월", "8월", "9월", "10월", "11월", "12월"},
		[12]string{"1월", "2월", "3월", "4월", "5월", "6월", "7월", "8월", "9월", "10월", "11월", "12월"},
		[7]string{"일요일", "월요일", "화요일", "수요일", "목요일", "금요일", "토요일"},
		[7]string{"월", "화", "수", "목", "금", "토", "일"},
		"{y}년 {m}", "{m} {d}일 {wd}", "{d}일 {wd}", "{m} {d}일 {wd} {t}", "{y}년 {m} {d}일 {wd} {t}"},
	"id": {
		[12]string{"Januari", "Februari", "Maret", "April", "Mei", "Juni", "Juli", "Agustus", "September", "Oktober", "November", "Desember"},
		[12]string{"Januari", "Februari", "Maret", "April", "Mei", "Juni", "Juli", "Agustus", "September", "Oktober", "November", "Desember"},
		[7]string{"Minggu", "Senin", "Selasa", "Rabu", "Kamis", "Jumat", "Sabtu"},
		[7]string{"Sn", "Sl", "Rb", "Km", "Jm", "Sb", "Mg"},
		"{m} {y}", "{wd}, {d} {m}", "{wd}, {d}", "{wd}, {d} {m}, {t}", "{wd}, {d} {m} {y}, {t}"},
	"hi": {
		[12]string{"जनवरी", "फ़रवरी", "मार्च", "अप्रैल", "मई", "जून", "जुलाई", "अगस्त", "सितंबर", "अक्तूबर", "नवंबर", "दिसंबर"},
		[12]string{"जनवरी", "फ़रवरी", "मार्च", "अप्रैल", "मई", "जून", "जुलाई", "अगस्त", "सितंबर", "अक्तूबर", "नवंबर", "दिसंबर"},
		[7]string{"रविवार", "सोमवार", "मंगलवार", "बुधवार", "गुरुवार", "शुक्रवार", "शनिवार"},
		[7]string{"सो", "मं", "बु", "गु", "शु", "श", "र"},
		"{m} {y}", "{wd}, {d} {m}", "{wd}, {d}", "{wd}, {d} {m}, {t}", "{wd}, {d} {m} {y}, {t}"},
}

func namesOf(lang string) dateNames {
	if n, ok := dates[lang]; ok {
		return n
	}
	return dates["en"]
}

// Date is t written in lang, in form, as the clock of t's location reads.
func Date(lang string, t time.Time, form DateForm) string {
	n := namesOf(lang)
	pattern, month := n.dayMonthAt, n.months[t.Month()-1]
	switch form {
	case MonthYear:
		pattern, month = n.monthYear, n.monthsAlone[t.Month()-1]
	case DayMonth:
		pattern = n.dayMonth
	case Day:
		pattern = n.day
	case DayMonthYearAt:
		pattern = n.dayMonthYearAt
	}
	return strings.NewReplacer("{wd}", n.days[t.Weekday()], "{d}", strconv.Itoa(t.Day()), "{m}", month,
		"{y}", strconv.Itoa(t.Year()), "{t}", t.Format("15:04")).Replace(pattern)
}

// WeekdayHeads is the calendar's column heads in lang, Monday first.
func WeekdayHeads(lang string) [7]string { return namesOf(lang).short }
