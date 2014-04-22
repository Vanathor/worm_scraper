package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/codegangsta/cli"
	"github.com/puerkitobio/goquery"
)

const (
	MainSite        = "https://parahumans.wordpress.com/"
	TableOfContents = "https://parahumans.wordpress.com/table-of-contents/"
)

type Arc struct {
	Identifier string
	Title      string
	Chapters   []Chapter
}

type Chapter struct {
	Title      string
	Url        string
	Tags       []string
	Paragraphs []Paragraph
	Retries    int
	DatePosted string
}

type Paragraph string

// Format the paragraph
func (p *Paragraph) Format() {
	s := string(*p)

	// Handle emphasis
	s = strings.Replace(s, "<em>", "*", -1)
	s = strings.Replace(s, "</em>", "*", -1)
	s = strings.Replace(s, "<i>", "*", -1)
	s = strings.Replace(s, "</i>", "*", -1)

	// Handle bold
	s = strings.Replace(s, "<strong>", "**", -1)
	s = strings.Replace(s, "</strong>", "**", -1)
	s = strings.Replace(s, "<b>", "**", -1)
	s = strings.Replace(s, "</b>", "**", -1)

	// Remove new lines
	s = strings.Replace(s, "\n", "", -1)

	// And random double spaces
	s = strings.Replace(s, ".  ", ". ", -1)

	*p = Paragraph(s)
}

// Return the Arc that the given chapter belongs to
func (ch *Chapter) WhichArc(arcList []*Arc) (*Arc, error) {
	for _, arc := range arcList {
		if strings.Replace(ch.Title[:2], ".", "", -1) == arc.Identifier {
			return arc, nil
		}
	}
	return &Arc{}, errors.New("chapter '" + ch.Title + "' did not match any Arcs")
}

// Parse a chapter and return it
func (ch *Chapter) Parse(done chan bool) {
	if ch.Retries > 3 {
		panic("Chapter url '" + ch.Url + "' has timed out too many times")
	}
	// Get the chapter
	if strings.HasPrefix(ch.Url, "http") == false {
		// Make sure it begins with http so goquery can use it
		ch.Url = "https://" + ch.Url
	}
	doc, err := goquery.NewDocument(ch.Url)
	if err != nil {
		// Try again
		ch.Retries++
		go ch.Parse(done)
		return
	}

	// Set the new chapter title
	ch.Title = doc.Find("h1.entry-title").Text()

	// Set the tags
	doc.Find(".entry-meta a[rel=tag]").Each(func(_ int, s *goquery.Selection) {
		ch.Tags = append(ch.Tags, s.Text())
	})

	// Get the date it was posted
	ch.DatePosted = doc.Find("time.entry-date").Text()

	// Now we'll get all the paragraphs
	doc.Find(".entry-content > p").Each(func(_ int, s *goquery.Selection) {
		// Check for the previous/next links
		if len(s.Find("a").Nodes) > 0 {
			return
		}

		// Get the paragraph HTML
		st, _ := s.Html()
		para := Paragraph("")

		// Get the actual paragraph
		if val, exists := s.Attr("padding-left"); exists && val == "30px" {
			// Check to see if the paragraph is special (indented) block
			para = Paragraph("    " + st)
		} else if val, exists := s.Attr("text-align"); exists && val == "center" {
			// Otherwise check to see if it's a separator paragraph
			para = Paragraph("----------")
		} else {
			// It's just a normal paragraph in this case
			para = Paragraph(st)
		}

		// And add the paragraph to the chapter
		para.Format()
		ch.Paragraphs = append(ch.Paragraphs, para)
	})

	// Finally, let's signal a success
	done <- true
}

// Return a slice of Arcs extracted from the table of contents
func ParseArcs(s string) []*Arc {
	arcs := []*Arc{}
	r, _ := regexp.Compile(`[0-9]+`)
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Arc") {
			arcs = append(arcs, &Arc{
				Identifier: r.FindString(line),
				Title:      line,
			})
		} else if strings.HasPrefix(line, "Epilogue") {
			arcs = append(arcs, &Arc{
				Identifier: "E",
				Title:      line,
			})
		}
	}
	return arcs
}

func main() {
	// Define the app
	app := cli.NewApp()
	app.Name = "Worm Scraper"
	app.Usage = "A tool to let you get an updated EPUB copy of the serial web novel Worm, by Wildbow"
	app.Version = "1.0"
	app.Author = "Benjamin Harris"

	// Define the application flags
	app.Flags = []cli.Flag{
		cli.BoolFlag{"pdf", "Save the book as a PDF instead of an EPUB, if possible"},
		cli.BoolFlag{"with-link", "Include a link to the chapter online"},
		cli.BoolFlag{"with-tags", "Include the tags each chapter was posted under"},
		cli.BoolFlag{"with-date", "Include the date each chapter was posted"},
	}

	// The heart of the application
	app.Action = func(context *cli.Context) {
		// Starting the program
		fmt.Println("Starting to scrape Worm")

		// Get the list of arcs from the table of contents
		fmt.Println("Gathering links from table of contents...")
		contents, err := goquery.NewDocument(TableOfContents)
		if err != nil {
			panic("Failed to get the table of contents! " + err.Error())
		}

		// Parse the arcs
		arcs := ParseArcs(contents.Find(".entry-content").Text())

		// Now get the links for the arc chapters
		contents.Find(".entry-content a:not([class*=share-icon])").Each(func(_ int, s *goquery.Selection) {
			ch := Chapter{}
			ch.Title = strings.Replace(strings.TrimSpace(s.Text()), "\n", "", -1)
			ch.Url, _ = s.Attr("href")

			if ch.Title == "" {
				return
			}

			arc, _ := ch.WhichArc(arcs)
			arc.Chapters = append(arc.Chapters, ch)
		})

		// Manually add missing chapter in Epilogue
		c := Chapter{
			Title: "E.2",
			Url:   "https://parahumans.wordpress.com/2013/11/05/teneral-e-2/",
		}
		a, _ := c.WhichArc(arcs)
		a.Chapters = append(a.Chapters, c)
		copy(a.Chapters[1+1:], a.Chapters[1:])
		a.Chapters[1] = c

		// Now start getting the chapters
		chapters := 0
		done := make(chan bool)
		for _, arc := range arcs {
			for i, _ := range arc.Chapters {
				chapters++
				go arc.Chapters[i].Parse(done)
			}
		}

		fmt.Println("Starting to parse", chapters, "chapters")
		fmt.Print("Finished: ")

		totalChapters := chapters
		for {
			select {
			case <-done:
				chapters--
				fmt.Print(totalChapters-chapters, ",")
			}
			if chapters == 0 {
				// We're done with all the chapters
				close(done)
				fmt.Println()
				break
			}
		}

		// And let's write all this stuff to a file now
		fmt.Println("Saving results to file...")
		f, err := os.OpenFile("Worm.md", os.O_RDWR|os.O_CREATE|os.O_EXCL, 0666)
		if err != nil {
			panic(err)
		}
		defer f.Close()

		// Define pagebreak
		PageBreak := "\n\n"

		// Write the cover
		f.WriteString("# Worm\n\n")
		f.WriteString("By Wildbow\n\n")
		f.WriteString("Website: " + MainSite)

		// Now loop through the Arcs
		for _, arc := range arcs {
			f.WriteString(PageBreak + arc.Title)
			for _, chapter := range arc.Chapters {
				f.WriteString("\n\n")
				f.WriteString("## " + chapter.Title + "\n\n")
				if context.Bool("with-tags") {
					f.WriteString("**Tags:** " + strings.Join(chapter.Tags, ", ") + "  ")
				}
				if context.Bool("with-date") {
					f.WriteString("**Date:** " + chapter.DatePosted + "  ")
				}
				if context.Bool("with-link") {
					f.WriteString("**Link:** " + chapter.Url + "  ")
				}
				f.WriteString("\n\n")

				// Now save the chapter's paragraphs
				for _, p := range chapter.Paragraphs {
					f.WriteString(string(p) + "\n\n")
				}
			}
		}

		// Now let's try to convert the markdown file into an ebook format (epub, pdf)
		fmt.Print("Attempting to convert Markdown file... ")
		cmdText := []string{"-S", "Worm.md", "--epub-chapter-level", "2", "-o", "Worm.epub"}
		if context.Bool("pdf") {
			cmdText = []string{"Worm.md", "-o", "Worm.pdf"}
			PageBreak = `<div style="page-break-after: always;"></div>`
		}
		cmd := exec.Command("pandoc", cmdText...)
		err = cmd.Run()
		if err != nil {
			fmt.Println("Conversion failed! Make sure you've installed Pandoc (http://johnmacfarlane.net/pandoc/installing.html) if you want to convert the generated Markdown file to an ebook compatible format. In the meantime, we've left you the Markdown file.")
		} else {
			_ = os.Remove("Worm.md")
			fmt.Println("Completed!")
		}
	}

	// Run the application
	app.Run(os.Args)
}
