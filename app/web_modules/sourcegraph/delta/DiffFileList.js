import React from "react";
import DiffStatScale from "sourcegraph/delta/DiffStatScale";
import {isDevNull} from "sourcegraph/delta/util";
import styles from "sourcegraph/delta/styles/DiffFileList.css";
import CSSModules from "react-css-modules";
import {TriangleRightIcon, TriangleDownIcon} from "sourcegraph/components/Icons";
import {Heading, Hero, Panel, Button} from "sourcegraph/components";

class DiffFileList extends React.Component {
	static propTypes = {
		files: React.PropTypes.arrayOf(React.PropTypes.object),
		stats: React.PropTypes.object.isRequired,
	};

	state = {closed: false};

	render() {
		return (
			<Panel styleName="container">
				<a styleName="header" onClick={() => this.setState({closed: !this.state.closed})}>
					<div>
						{this.state.closed ? <TriangleRightIcon /> : <TriangleDownIcon />}&nbsp;
						<strong>Files</strong> <span styleName="count">({this.props.files.length})</span>
					</div>
					<div styleName="stats">
						<span styleName="additions-color">+{this.props.stats.Added}</span>
						<span styleName="deletions-color">-{this.props.stats.Deleted}</span>
					</div>
				</a>

				<ul styleName={`files ${this.state.closed ? "closed" : ""}`}>
					{this.props.files.map((fd, i) => (
						<li key={fd.OrigName + fd.NewName}>
							<a href={`#F${i}`} styleName="file">
								{isDevNull(fd.OrigName) ? <code styleName="change-type additions-color">+</code> : null}
								{isDevNull(fd.NewName) ? <code styleName="change-type deletions-color">&minus;</code> : null}
								{!isDevNull(fd.OrigName) && !isDevNull(fd.NewName) ? <code styleName="change-type changes-color">&bull;</code> : null}

								{!isDevNull(fd.OrigName) && !isDevNull(fd.NewName) && fd.OrigName !== fd.NewName ? (
									<span>{fd.OrigName} &rarr;&nbsp;</span>
								) : null}

								{isDevNull(fd.NewName) ? fd.OrigName : fd.NewName}

								<div styleName="stats">
									<span styleName="additions-color">+{this.props.stats.Added}</span>
									<span styleName="deletions-color">-{this.props.stats.Deleted}</span>
									<DiffStatScale Stat={this.props.stats} />
								</div>
							</a>
						</li>
					))}
				</ul>
			</Panel>
		);
	}
}

export default CSSModules(DiffFileList, styles, {allowMultiple: true});
