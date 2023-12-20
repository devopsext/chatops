package bot

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/devopsext/chatops/common"
	sreCommon "github.com/devopsext/sre/common"
	"github.com/devopsext/utils"
	"github.com/shomali11/proper"
	"github.com/slack-go/slack"
	"github.com/slack-io/slacker"
)

type SlackOptions struct {
	BotToken      string
	AppToken      string
	Debug         bool
	ReplyInThread bool
}

type Slack struct {
	options    SlackOptions
	processors *common.Processors
	client     *slacker.Slacker
	logger     sreCommon.Logger
}

type RichTextQuoteElement struct {
	Type   slack.RichTextElementType `json:"type"`
	Text   string                    `json:"text,omitempty"`
	UserID string                    `json:"user_id,omitempty"`
}

type RichTextQuote struct {
	Type     slack.RichTextElementType `json:"type"`
	Elements []*RichTextQuoteElement   `json:"elements"`
}

func (r RichTextQuote) RichTextElementType() slack.RichTextElementType {
	return r.Type
}

func (r RichTextQuoteElement) RichTextElementType() slack.RichTextElementType {
	return r.Type
}

func (s *Slack) Name() string {
	return "Slack"
}

func (s *Slack) replyDefaultOptions() []slacker.ReplyOption {

	opts := []slacker.ReplyOption{
		slacker.WithInThread(s.options.ReplyInThread),
	}
	return opts
}

func (s *Slack) reply(cc *slacker.CommandContext, options []slacker.ReplyOption, message string, error bool) {

	userID := cc.Event().UserID
	channelID := cc.Event().ChannelID
	threadTS := cc.Event().ThreadTimeStamp

	text := cc.Event().Text
	items := strings.Split(text, ">")
	if len(items) > 1 {
		text = items[1]
	}

	replyInThread := s.options.ReplyInThread
	if utils.IsEmpty(threadTS) {
		threadTS = cc.Event().TimeStamp
	} else {
		replyInThread = true
	}

	opts := []slacker.PostOption{}
	if error {
		attachments := []slack.Attachment{}
		attachments = append(attachments, slack.Attachment{
			Color: "danger",
			Text:  message,
		})
		opts = append(opts, slacker.SetAttachments(attachments))
	}

	if replyInThread {
		opts = append(opts, slacker.SetThreadTS(threadTS))
	}

	elements := []slack.RichTextElement{
		// add quote
		&RichTextQuote{Type: slack.RTEQuote, Elements: []*RichTextQuoteElement{
			{Type: "user", UserID: userID},
			{Type: "text", Text: text},
		}},
	}

	blocks := []slack.Block{
		slack.NewRichTextBlock("quote", elements...),
	}

	if !error {
		blocks = append(blocks, slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn", message, false, false),
			[]*slack.TextBlockObject{},
			nil,
		))
	}

	_, err := cc.Response().PostBlocks(channelID, blocks, opts...)
	if err != nil {
		s.logger.Error(err)
	}
}

func (s *Slack) replyMessage(cc *slacker.CommandContext, options []slacker.ReplyOption, message string) {
	s.reply(cc, options, message, false)
}

func (s *Slack) replyError(cc *slacker.CommandContext, options []slacker.ReplyOption, err error) {
	s.reply(cc, options, err.Error(), true)
}

func (s *Slack) buildCommand(name string, params []string) string {

	r := name
	if len(params) > 0 {
		arr := []string{}
		for _, v := range params {
			arr = append(arr, fmt.Sprintf("{%s}", v))
		}
		r = fmt.Sprintf("%s %s", r, strings.Join(arr, " "))
	}
	return r
}

func (s *Slack) convertProperties(params []string, props *proper.Properties) common.ExecuteParams {

	r := make(common.ExecuteParams)
	if props == nil {
		return r
	}
	for _, v := range params {
		s := props.StringParam(v, "")
		if !utils.IsEmpty(s) {
			r[v] = s
		}
	}
	return r
}

func (s *Slack) defaultCommandDefinition(cmd common.Command, groupName string) *slacker.CommandDefinition {

	cName := cmd.Name()
	params := cmd.Params()
	def := &slacker.CommandDefinition{
		Command:     s.buildCommand(cName, params),
		Description: cmd.Description(),
		Handler: func(cc *slacker.CommandContext) {

			r := cc.Request()
			if r == nil {
				s.logger.Debug("Slack command %s request %v is empty", groupName, r)
				return
			}

			replyOpts := s.replyDefaultOptions()
			cr, err := cmd.Execute(s, s.convertProperties(params, r.Properties()))
			if err != nil {
				s.logger.Error("Slack command %s request %s execution error: %s", groupName, cName, err)
				s.replyError(cc, replyOpts, err)
				return
			}
			if utils.IsEmpty(cr) {
				err := fmt.Errorf("Slack command %s request %s no response", groupName, cName)
				s.logger.Error(err)
				s.replyError(cc, replyOpts, err)
				return
			}

			// add attachements if some
			attachments := cr.Attachments()
			if len(attachments) > 0 {
				var replyAttachments []slack.Attachment
				for _, a := range attachments {
					replyAttachments = append(replyAttachments, slack.Attachment{
						Pretext: a.Text,
						Title:   a.Title,
						Text:    string(a.Data),
					})
				}
				if len(replyAttachments) > 0 {
					replyOpts = append(replyOpts, slacker.WithAttachments(replyAttachments))
				}
			}

			message, err := cr.Message()
			if err != nil {
				err = fmt.Errorf("Slack command %s response %s message error: %s", groupName, cName, err)
				s.replyError(cc, replyOpts, err)
				return
			}
			s.replyMessage(cc, replyOpts, message)
		},
	}
	return def
}

func (s *Slack) start() {

	client := slacker.NewClient(s.options.BotToken, s.options.AppToken, slacker.WithDebug(s.options.Debug))

	for _, p := range s.processors.Items() {

		pName := p.Name()
		commands := p.Commands()

		if len(commands) == 0 {
			client.AddCommand(s.defaultCommandDefinition(p, pName))
		} else {

			group := client.AddCommandGroup(pName)
			for _, c := range commands {
				group.AddCommand(s.defaultCommandDefinition(c, fmt.Sprintf("%s/%s", pName, c.Name())))
			}
		}
	}
	s.client = client

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := client.Listen(ctx)
	if err != nil {
		s.logger.Error(err)
		return
	}
}

func (t *Slack) Start(wg *sync.WaitGroup) {

	if wg == nil {
		t.start()
		return
	}

	wg.Add(1)

	go func(wg *sync.WaitGroup) {

		defer wg.Done()
		t.start()
	}(wg)
}

func NewSlack(options SlackOptions, observability *common.Observability, processors *common.Processors) *Slack {

	return &Slack{
		options:    options,
		processors: processors,
		logger:     observability.Logs(),
	}
}
