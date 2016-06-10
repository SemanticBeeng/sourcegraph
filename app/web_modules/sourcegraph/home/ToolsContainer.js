import React from "react";
import CSSModules from "react-css-modules";
import styles from "./styles/Tools.css";
import ToolsHomeComponent from "./ToolsHomeComponent";
import Container from "sourcegraph/Container";
import UserStore from "sourcegraph/user/UserStore";
import "sourcegraph/user/UserBackend"; // for side effects
import {Button} from "sourcegraph/components";
import * as UserActions from "sourcegraph/user/UserActions";
import Dispatcher from "sourcegraph/Dispatcher";

class ToolsContainer extends Container {
	static propTypes = {
		location: React.PropTypes.object,
	};

	reconcileState(state, props, context) {
		Object.assign(state, props);
	}

	stores() { return [UserStore]; }

	_emailSubscribe() {
		console.log("sending off");
		Dispatcher.Backends.dispatch(new UserActions.SubmitEmailSubscription(
			"kingy895@gmail.com",
			"Matt",
			"King",
			"Swift",
			"dd6c4706a1",
		));
	}

	render() {
		return (<div>
			<Button color="purple" disabled={false} onClick={this._emailSubscribe.bind(this)}>Install</Button>
			<ToolsHomeComponent location={this.props.location}/>
		</div>);
	}
}

export default CSSModules(ToolsContainer, styles);
